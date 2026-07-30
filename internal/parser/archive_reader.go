package parser

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"sort"
	"strings"

	"github.com/go-faster/city"
)

type archiveReader interface {
	FileNames() []string
	ReadFile(name string) ([]byte, error)
	Close() error
}

func openArchiveReader(archivePath string) (archiveReader, error) {
	version, isSCS, err := detectSCSArchiveVersion(archivePath)
	if err != nil {
		return nil, err
	}
	if isSCS {
		return newSCSArchiveReader(archivePath, version)
	}
	return newZipArchiveReader(archivePath)
}

func detectSCSArchiveVersion(path string) (version uint16, isSCS bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()

	header := make([]byte, 6)
	if _, err := io.ReadFull(f, header); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if string(header[:4]) != "SCS#" {
		return 0, false, nil
	}
	return binary.LittleEndian.Uint16(header[4:6]), true, nil
}

type zipArchiveReader struct {
	reader    *zip.ReadCloser
	files     map[string]*zip.File
	fileNames []string
}

func newZipArchiveReader(archivePath string) (archiveReader, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, err
	}

	files := map[string]*zip.File{}
	for _, file := range r.File {
		if file.FileInfo().IsDir() {
			continue
		}
		name := normalizeArchivePath(file.Name)
		name = strings.ToLower(name)
		if name == "" {
			continue
		}
		files[name] = file
	}
	fileNames := make([]string, 0, len(files))
	for name := range files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)

	return &zipArchiveReader{
		reader:    r,
		files:     files,
		fileNames: fileNames,
	}, nil
}

func (r *zipArchiveReader) FileNames() []string {
	return append([]string{}, r.fileNames...)
}

func (r *zipArchiveReader) ReadFile(name string) ([]byte, error) {
	name = strings.ToLower(normalizeArchivePath(name))
	file, ok := r.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	return readAll(rc)
}

func (r *zipArchiveReader) Close() error {
	return r.reader.Close()
}

type scsCompression uint8

const (
	scsCompressionNone           scsCompression = 0
	scsCompressionZlib           scsCompression = 1
	scsCompressionZlibHeaderless scsCompression = 2
	scsCompressionGDeflate       scsCompression = 3
	scsCompressionZstd           scsCompression = 4
)

const (
	scsMetadataTypePlain     uint8 = 1 << 7
	scsMetadataTypeDirectory uint8 = scsMetadataTypePlain | 1
	scsMetadataTypeMipTail   uint8 = scsMetadataTypePlain | 4
)

type scsEntry struct {
	hash           uint64
	offset         int64
	compressedSize uint32
	size           uint32
	compression    scsCompression
	isDirectory    bool
}

type scsDirectoryListing struct {
	subdirectories []string
	files          []string
}

type scsArchiveReader struct {
	file          *os.File
	version       uint16
	salt          int16
	directories   map[uint64]scsEntry
	filesByHash   map[uint64]scsEntry
	filesByName   map[string]scsEntry
	fileNames     []string
	directoryData map[uint64]scsDirectoryListing
}

func newSCSArchiveReader(archivePath string, version uint16) (archiveReader, error) {
	if version != 1 && version != 2 {
		return nil, fmt.Errorf("unsupported SCS archive version %d", version)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}

	reader := &scsArchiveReader{
		file:          f,
		version:       version,
		directories:   map[uint64]scsEntry{},
		filesByHash:   map[uint64]scsEntry{},
		filesByName:   map[string]scsEntry{},
		directoryData: map[uint64]scsDirectoryListing{},
	}

	if err := reader.parseArchiveEntries(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := reader.buildFileIndex(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return reader, nil
}

func (r *scsArchiveReader) FileNames() []string {
	return append([]string{}, r.fileNames...)
}

func (r *scsArchiveReader) ReadFile(name string) ([]byte, error) {
	name = strings.ToLower(normalizeArchivePath(name))
	entry, ok := r.filesByName[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return r.readEntryData(entry)
}

func (r *scsArchiveReader) Close() error {
	return r.file.Close()
}

func (r *scsArchiveReader) parseArchiveEntries() error {
	switch r.version {
	case 1:
		return r.parseV1Entries()
	case 2:
		return r.parseV2Entries()
	default:
		return fmt.Errorf("unsupported SCS archive version %d", r.version)
	}
}

func (r *scsArchiveReader) parseV1Entries() error {
	const headerSize = 20
	header, err := r.readRaw(0, headerSize)
	if err != nil {
		return err
	}

	if string(header[0:4]) != "SCS#" {
		return errors.New("invalid SCS v1 archive header")
	}
	if version := binary.LittleEndian.Uint16(header[4:6]); version != 1 {
		return fmt.Errorf("invalid SCS v1 archive version: %d", version)
	}
	r.salt = int16(binary.LittleEndian.Uint16(header[6:8]))
	if string(header[8:12]) != "CITY" {
		return fmt.Errorf("unsupported SCS v1 hash method: %q", string(header[8:12]))
	}

	numEntries := int(binary.LittleEndian.Uint32(header[12:16]))
	entriesOffset := int64(binary.LittleEndian.Uint32(header[16:20]))
	if numEntries < 0 {
		return errors.New("invalid SCS v1 entry count")
	}

	const entrySize = 32
	table, err := r.readRaw(entriesOffset, numEntries*entrySize)
	if err != nil {
		return err
	}

	for i := 0; i < numEntries; i++ {
		offset := i * entrySize
		hash := binary.LittleEndian.Uint64(table[offset : offset+8])
		dataOffset := int64(binary.LittleEndian.Uint64(table[offset+8 : offset+16]))
		flags := binary.LittleEndian.Uint32(table[offset+16 : offset+20])
		size := binary.LittleEndian.Uint32(table[offset+24 : offset+28])
		compressedSize := binary.LittleEndian.Uint32(table[offset+28 : offset+32])
		isCompressed := flags&0b10 != 0
		isDirectory := flags&0b1 != 0
		compression := scsCompressionNone
		if isCompressed {
			compression = scsCompressionZlib
		}
		if compressedSize == 0 && size > 0 {
			compressedSize = size
		}

		entry := scsEntry{
			hash:           hash,
			offset:         dataOffset,
			compressedSize: compressedSize,
			size:           size,
			compression:    compression,
			isDirectory:    isDirectory,
		}
		if entry.isDirectory {
			r.directories[entry.hash] = entry
		} else {
			r.filesByHash[entry.hash] = entry
		}
	}
	return nil
}

type scsV2EntryHeader struct {
	hash          uint64
	metadataIndex uint32
	metadataCount uint16
	flags         uint8
}

type scsV2Metadata struct {
	metaType       uint8
	offset         int64
	compressedSize uint32
	size           uint32
	compression    scsCompression
}

func (r *scsArchiveReader) parseV2Entries() error {
	const headerSize = 53
	header, err := r.readRaw(0, headerSize)
	if err != nil {
		return err
	}

	if string(header[0:4]) != "SCS#" {
		return errors.New("invalid SCS v2 archive header")
	}
	if version := binary.LittleEndian.Uint16(header[4:6]); version != 2 {
		return fmt.Errorf("invalid SCS v2 archive version: %d", version)
	}
	r.salt = int16(binary.LittleEndian.Uint16(header[6:8]))
	if string(header[8:12]) != "CITY" {
		return fmt.Errorf("unsupported SCS v2 hash method: %q", string(header[8:12]))
	}

	entryTableCount := int(binary.LittleEndian.Uint32(header[12:16]))
	entryTableCompressedSize := binary.LittleEndian.Uint32(header[16:20])
	metadataTableSize := binary.LittleEndian.Uint32(header[20:24])
	metadataTableCompressedSize := binary.LittleEndian.Uint32(header[24:28])
	entryTableOffset := int64(binary.LittleEndian.Uint64(header[28:36]))
	metadataTableOffset := int64(binary.LittleEndian.Uint64(header[36:44]))

	entryTable, err := r.readSection(
		entryTableOffset,
		entryTableCompressedSize,
		uint32(entryTableCount*16),
	)
	if err != nil {
		return err
	}
	if len(entryTable) < entryTableCount*16 {
		return errors.New("incomplete SCS v2 entry table")
	}

	entryHeaders := make([]scsV2EntryHeader, 0, entryTableCount)
	for i := 0; i < entryTableCount; i++ {
		offset := i * 16
		entryHeaders = append(entryHeaders, scsV2EntryHeader{
			hash:          binary.LittleEndian.Uint64(entryTable[offset : offset+8]),
			metadataIndex: binary.LittleEndian.Uint32(entryTable[offset+8 : offset+12]),
			metadataCount: binary.LittleEndian.Uint16(entryTable[offset+12 : offset+14]),
			flags:         entryTable[offset+14],
		})
	}

	metadataTable, err := r.readSection(
		metadataTableOffset,
		metadataTableCompressedSize,
		metadataTableSize,
	)
	if err != nil {
		return err
	}

	metadataByIndex := map[uint32]scsV2Metadata{}
	for _, header := range entryHeaders {
		for i := 0; i < int(header.metadataCount); i++ {
			index := header.metadataIndex + uint32(i)
			if _, exists := metadataByIndex[index]; exists {
				continue
			}
			metadataHeaderOffset := int(index) * 4
			if metadataHeaderOffset+4 > len(metadataTable) {
				continue
			}
			descriptorIndex := uint32(metadataTable[metadataHeaderOffset]) |
				uint32(metadataTable[metadataHeaderOffset+1])<<8 |
				uint32(metadataTable[metadataHeaderOffset+2])<<16
			metaType := metadataTable[metadataHeaderOffset+3]

			switch metaType {
			case scsMetadataTypePlain, scsMetadataTypeDirectory, scsMetadataTypeMipTail:
			default:
				continue
			}

			descriptorOffset := int(descriptorIndex) * 4
			if descriptorOffset+16 > len(metadataTable) {
				continue
			}

			packedCompression := binary.LittleEndian.Uint32(metadataTable[descriptorOffset : descriptorOffset+4])
			size := binary.LittleEndian.Uint32(metadataTable[descriptorOffset+4 : descriptorOffset+8])
			offsetUnits := binary.LittleEndian.Uint32(metadataTable[descriptorOffset+12 : descriptorOffset+16])
			metadataByIndex[index] = scsV2Metadata{
				metaType:       metaType,
				offset:         int64(uint64(offsetUnits) * 16),
				compressedSize: packedCompression & 0x0fff_ffff,
				size:           size,
				compression:    scsCompression((packedCompression & 0xf000_0000) >> 28),
			}
		}
	}

	for _, header := range entryHeaders {
		metadata, ok := pickV2Metadata(metadataByIndex, header.metadataIndex, header.metadataCount, header.flags&0b1 != 0)
		if !ok {
			continue
		}
		entry := scsEntry{
			hash:           header.hash,
			offset:         metadata.offset,
			compressedSize: metadata.compressedSize,
			size:           metadata.size,
			compression:    metadata.compression,
			isDirectory:    header.flags&0b1 != 0,
		}
		if entry.isDirectory {
			r.directories[entry.hash] = entry
		} else {
			r.filesByHash[entry.hash] = entry
		}
	}
	return nil
}

func pickV2Metadata(metadataByIndex map[uint32]scsV2Metadata, start uint32, count uint16, isDirectory bool) (scsV2Metadata, bool) {
	fallback := scsV2Metadata{}
	hasFallback := false
	for i := 0; i < int(count); i++ {
		meta, ok := metadataByIndex[start+uint32(i)]
		if !ok {
			continue
		}
		if isDirectory && meta.metaType == scsMetadataTypeDirectory {
			return meta, true
		}
		if !isDirectory && (meta.metaType == scsMetadataTypePlain || meta.metaType == scsMetadataTypeMipTail) {
			return meta, true
		}
		if !hasFallback && (meta.metaType == scsMetadataTypePlain || meta.metaType == scsMetadataTypeDirectory || meta.metaType == scsMetadataTypeMipTail) {
			fallback = meta
			hasFallback = true
		}
	}
	return fallback, hasFallback
}

func (r *scsArchiveReader) buildFileIndex() error {
	visited := map[string]bool{}
	if _, ok := r.directories[r.hashPath("")]; ok {
		r.walkDirectory("", visited)
	} else {
		for _, root := range []string{"def", "map", "locale", "mod"} {
			if _, ok := r.directories[r.hashPath(root)]; ok {
				r.walkDirectory(root, visited)
			}
		}
	}

	for _, explicit := range []string{"version.sii", "manifest.sii", "mod_description.sii", "mod/manifest.sii", "mod/mod_description.sii"} {
		hash := r.hashPath(explicit)
		entry, ok := r.filesByHash[hash]
		if !ok {
			continue
		}
		r.filesByName[strings.ToLower(explicit)] = entry
	}

	fileNames := make([]string, 0, len(r.filesByName))
	for name := range r.filesByName {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	r.fileNames = fileNames
	return nil
}

func (r *scsArchiveReader) walkDirectory(dirPath string, visited map[string]bool) {
	dirPath = normalizeArchivePath(dirPath)
	visitedKey := strings.ToLower(dirPath)
	if visited[visitedKey] {
		return
	}
	visited[visitedKey] = true

	dirEntry, ok := r.directories[r.hashPath(dirPath)]
	if !ok {
		return
	}
	subdirs, files, err := r.readDirectoryListing(dirEntry)
	if err != nil {
		return
	}

	for _, file := range files {
		fullPath := joinArchivePath(dirPath, file)
		if fullPath == "" {
			continue
		}
		entry, ok := r.filesByHash[r.hashPath(fullPath)]
		if !ok {
			continue
		}
		r.filesByName[strings.ToLower(fullPath)] = entry
	}
	for _, subdirectory := range subdirs {
		r.walkDirectory(joinArchivePath(dirPath, subdirectory), visited)
	}
}

func (r *scsArchiveReader) readDirectoryListing(entry scsEntry) ([]string, []string, error) {
	if listing, ok := r.directoryData[entry.hash]; ok {
		return listing.subdirectories, listing.files, nil
	}

	data, err := r.readEntryData(entry)
	if err != nil {
		return nil, nil, err
	}

	var subdirectories []string
	var files []string
	switch r.version {
	case 1:
		subdirectories, files = parseV1DirectoryData(data)
	case 2:
		subdirectories, files, err = parseV2DirectoryData(data)
	default:
		return nil, nil, fmt.Errorf("unsupported SCS archive version %d", r.version)
	}
	if err != nil {
		return nil, nil, err
	}

	listing := scsDirectoryListing{
		subdirectories: subdirectories,
		files:          files,
	}
	r.directoryData[entry.hash] = listing
	return listing.subdirectories, listing.files, nil
}

func parseV1DirectoryData(data []byte) ([]string, []string) {
	subdirectories := make([]string, 0)
	files := make([]string, 0)
	lines := strings.FieldsFunc(string(data), func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	for _, line := range lines {
		line = normalizeArchivePath(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "*") {
			subdirectory := normalizeArchivePath(strings.TrimPrefix(line, "*"))
			if subdirectory != "" {
				subdirectories = append(subdirectories, subdirectory)
			}
			continue
		}
		files = append(files, line)
	}
	return subdirectories, files
}

func parseV2DirectoryData(data []byte) ([]string, []string, error) {
	if len(data) < 4 {
		return nil, nil, errors.New("invalid SCS v2 directory data")
	}
	stringCount := int(binary.LittleEndian.Uint32(data[:4]))
	if len(data) < 4+stringCount {
		return nil, nil, errors.New("invalid SCS v2 directory string table")
	}

	subdirectories := make([]string, 0, stringCount)
	files := make([]string, 0, stringCount)
	offset := 4 + stringCount
	for i := 0; i < stringCount; i++ {
		length := int(data[4+i])
		if offset+length > len(data) {
			return nil, nil, errors.New("invalid SCS v2 directory entry")
		}
		entry := strings.TrimSpace(string(data[offset : offset+length]))
		entry = strings.ReplaceAll(entry, "\\", "/")
		offset += length
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, "/") {
			subdir := normalizeArchivePath(strings.TrimPrefix(entry, "/"))
			if subdir != "" {
				subdirectories = append(subdirectories, subdir)
			}
			continue
		}
		file := normalizeArchivePath(entry)
		if file != "" {
			files = append(files, file)
		}
	}
	return subdirectories, files, nil
}

func (r *scsArchiveReader) hashPath(path string) uint64 {
	path = normalizeArchivePath(path)
	if r.salt == 0 {
		return city.Hash64([]byte(path))
	}
	return city.Hash64([]byte(fmt.Sprintf("%d%s", r.salt, path)))
}

func (r *scsArchiveReader) readEntryData(entry scsEntry) ([]byte, error) {
	rawData, err := r.readRaw(entry.offset, int(entry.compressedSize))
	if err != nil {
		return nil, err
	}
	switch entry.compression {
	case scsCompressionNone:
		if entry.size > 0 && int(entry.size) < len(rawData) {
			return rawData[:entry.size], nil
		}
		return rawData, nil
	case scsCompressionZlib:
		return inflateZlib(rawData)
	case scsCompressionZlibHeaderless:
		return inflateRawDeflate(rawData)
	case scsCompressionGDeflate:
		return nil, errors.New("unsupported SCS compression: gdeflate")
	case scsCompressionZstd:
		return nil, errors.New("unsupported SCS compression: zstd")
	default:
		return nil, fmt.Errorf("unsupported SCS compression: %d", entry.compression)
	}
}

func (r *scsArchiveReader) readSection(offset int64, compressedSize, uncompressedSize uint32) ([]byte, error) {
	data, err := r.readRaw(offset, int(compressedSize))
	if err != nil {
		return nil, err
	}
	if compressedSize == uncompressedSize {
		return data, nil
	}
	return inflateZlib(data)
}

func (r *scsArchiveReader) readRaw(offset int64, length int) ([]byte, error) {
	if length <= 0 {
		return []byte{}, nil
	}
	data := make([]byte, length)
	if _, err := r.file.ReadAt(data, offset); err != nil {
		return nil, err
	}
	return data, nil
}

func inflateZlib(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func inflateRawDeflate(data []byte) ([]byte, error) {
	reader := flate.NewReader(bytes.NewReader(data))
	defer reader.Close()
	return io.ReadAll(reader)
}

func normalizeArchivePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")
	if path == "." {
		return ""
	}
	return path
}

func joinArchivePath(parent, child string) string {
	parent = normalizeArchivePath(parent)
	child = normalizeArchivePath(child)
	if parent == "" {
		return child
	}
	if child == "" {
		return parent
	}
	return normalizeArchivePath(pathpkg.Join(parent, child))
}
