package parser

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-faster/city"
)

func TestReadSIIEntriesReadsSCSV1Archive(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "game.scs")
	mustWriteSCSV1(t, archivePath, map[string]string{
		"version.sii":                  `SiiNunit { version_data : .version { application: "ats" } }`,
		"def/country/test_country.sii": `SiiNunit { country_data : test_country { country_name: "Test Country" } }`,
	})

	entries, warnings := readSIIEntries([]string{archivePath})
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if entries["version.sii"] == "" {
		t.Fatalf("expected version.sii entry")
	}
	if entries["def/country/test_country.sii"] == "" {
		t.Fatalf("expected def/country/test_country.sii entry")
	}
}

func TestReadSIIEntriesReadsSCSV2Archive(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "game.scs")
	mustWriteSCSV2(t, archivePath, map[string]string{
		"version.sii":                  `SiiNunit { version_data : .version { application: "ats" } }`,
		"def/country/test_country.sii": `SiiNunit { country_data : test_country { country_name: "Test Country" } }`,
	})

	entries, warnings := readSIIEntries([]string{archivePath})
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if entries["version.sii"] == "" {
		t.Fatalf("expected version.sii entry")
	}
	if entries["def/country/test_country.sii"] == "" {
		t.Fatalf("expected def/country/test_country.sii entry")
	}
}

func TestReadSIIEntriesWarnsOnUnsupportedDirectoryCompression(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "game.scs")
	mustWriteSCSV2WithDirectoryCompression(t, archivePath, map[string]string{
		"version.sii": `SiiNunit { version_data : .version { application: "ats" } }`,
	}, scsCompressionGDeflate)

	entries, warnings := readSIIEntries([]string{archivePath})
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
	if len(warnings) == 0 {
		t.Fatalf("expected warning for unsupported directory compression")
	}
	if !strings.Contains(warnings[0], "unsupported SCS compression: gdeflate") {
		t.Fatalf("warning = %q, want unsupported compression message", warnings[0])
	}
}

func TestReadManifestMetadataReadsSCSArchive(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "mod.scs")
	mustWriteSCSV1(t, archivePath, map[string]string{
		"manifest.sii": `SiiNunit {
mod_package : .package {
 package_name: "test_mod"
 display_name: "Test Mod"
 dependencies[]: "dep_one"
 incompatible[]: "conflict_one"
}
}`,
	})

	metadata, warning := readManifestMetadata(archivePath)
	if warning != "" {
		t.Fatalf("unexpected warning: %s", warning)
	}
	if metadata == nil {
		t.Fatalf("expected metadata")
	}
	if metadata.PackageName != "test_mod" {
		t.Fatalf("PackageName = %q, want %q", metadata.PackageName, "test_mod")
	}
	if metadata.DisplayName != "Test Mod" {
		t.Fatalf("DisplayName = %q, want %q", metadata.DisplayName, "Test Mod")
	}
	if len(metadata.Dependencies) != 1 || metadata.Dependencies[0] != "dep_one" {
		t.Fatalf("Dependencies = %#v, want [dep_one]", metadata.Dependencies)
	}
	if len(metadata.Incompatible) != 1 || metadata.Incompatible[0] != "conflict_one" {
		t.Fatalf("Incompatible = %#v, want [conflict_one]", metadata.Incompatible)
	}
	if metadata.ManifestPath != "manifest.sii" {
		t.Fatalf("ManifestPath = %q, want %q", metadata.ManifestPath, "manifest.sii")
	}
}

type testDirectoryEntry struct {
	subdirectories []string
	files          []string
}

type testArchiveEntry struct {
	path        string
	data        []byte
	isDirectory bool
	offset      int64
}

func mustWriteSCSV1(t *testing.T, archivePath string, files map[string]string) {
	t.Helper()

	directories := buildTestDirectoryTree(files)
	entries := buildTestArchiveEntries(directories, files, false)

	const headerSize = 20
	const entrySize = 32
	entriesOffset := int64(headerSize)
	tableSize := int64(len(entries) * entrySize)
	dataOffset := entriesOffset + tableSize
	for idx := range entries {
		entries[idx].offset = dataOffset
		dataOffset += int64(len(entries[idx].data))
	}

	buffer := make([]byte, int(dataOffset))
	copy(buffer[0:4], "SCS#")
	binary.LittleEndian.PutUint16(buffer[4:6], 1)
	binary.LittleEndian.PutUint16(buffer[6:8], 0) // salt
	copy(buffer[8:12], "CITY")
	binary.LittleEndian.PutUint32(buffer[12:16], uint32(len(entries)))
	binary.LittleEndian.PutUint32(buffer[16:20], uint32(entriesOffset))

	for idx, entry := range entries {
		offset := headerSize + idx*entrySize
		binary.LittleEndian.PutUint64(buffer[offset:offset+8], city.Hash64([]byte(entry.path)))
		binary.LittleEndian.PutUint64(buffer[offset+8:offset+16], uint64(entry.offset))
		flags := uint32(0)
		if entry.isDirectory {
			flags |= 0b1
		}
		binary.LittleEndian.PutUint32(buffer[offset+16:offset+20], flags)
		binary.LittleEndian.PutUint32(buffer[offset+20:offset+24], 0) // crc
		binary.LittleEndian.PutUint32(buffer[offset+24:offset+28], uint32(len(entry.data)))
		binary.LittleEndian.PutUint32(buffer[offset+28:offset+32], uint32(len(entry.data)))
		copy(buffer[int(entry.offset):int(entry.offset)+len(entry.data)], entry.data)
	}

	if err := os.WriteFile(archivePath, buffer, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustWriteSCSV2(t *testing.T, archivePath string, files map[string]string) {
	t.Helper()
	mustWriteSCSV2WithDirectoryCompression(t, archivePath, files, scsCompressionNone)
}

func mustWriteSCSV2WithDirectoryCompression(t *testing.T, archivePath string, files map[string]string, directoryCompression scsCompression) {
	t.Helper()

	directories := buildTestDirectoryTree(files)
	entries := buildTestArchiveEntries(directories, files, true)

	const headerSize = 53
	const entrySize = 16
	const metadataHeaderSize = 4
	const metadataDescriptorSize = 16

	entryTableOffset := align16(headerSize)
	entryTableSize := len(entries) * entrySize
	metadataTableOffset := align16(entryTableOffset + entryTableSize)
	metadataTableSize := len(entries) * (metadataHeaderSize + metadataDescriptorSize)
	dataOffset := align16(metadataTableOffset + metadataTableSize)

	for idx := range entries {
		dataOffset = align16(dataOffset)
		entries[idx].offset = int64(dataOffset)
		dataOffset += len(entries[idx].data)
	}

	buffer := make([]byte, dataOffset)
	copy(buffer[0:4], "SCS#")
	binary.LittleEndian.PutUint16(buffer[4:6], 2)
	binary.LittleEndian.PutUint16(buffer[6:8], 0) // salt
	copy(buffer[8:12], "CITY")
	binary.LittleEndian.PutUint32(buffer[12:16], uint32(len(entries)))
	binary.LittleEndian.PutUint32(buffer[16:20], uint32(entryTableSize))
	binary.LittleEndian.PutUint32(buffer[20:24], uint32(metadataTableSize))
	binary.LittleEndian.PutUint32(buffer[24:28], uint32(metadataTableSize))
	binary.LittleEndian.PutUint64(buffer[28:36], uint64(entryTableOffset))
	binary.LittleEndian.PutUint64(buffer[36:44], uint64(metadataTableOffset))
	binary.LittleEndian.PutUint64(buffer[44:52], 0)
	buffer[52] = 0

	for idx, entry := range entries {
		offset := entryTableOffset + idx*entrySize
		binary.LittleEndian.PutUint64(buffer[offset:offset+8], city.Hash64([]byte(entry.path)))
		binary.LittleEndian.PutUint32(buffer[offset+8:offset+12], uint32(idx))
		binary.LittleEndian.PutUint16(buffer[offset+12:offset+14], 1)
		if entry.isDirectory {
			buffer[offset+14] = 0b1
		}
		buffer[offset+15] = 0
	}

	descriptorStart := metadataTableOffset + len(entries)*metadataHeaderSize
	for idx, entry := range entries {
		headerOffset := metadataTableOffset + idx*metadataHeaderSize
		descriptorOffset := descriptorStart + idx*metadataDescriptorSize
		descriptorIndex := uint32((descriptorOffset - metadataTableOffset) / 4)
		putUint24LE(buffer[headerOffset:headerOffset+3], descriptorIndex)
		if entry.isDirectory {
			buffer[headerOffset+3] = scsMetadataTypeDirectory
		} else {
			buffer[headerOffset+3] = scsMetadataTypePlain
		}

		packedCompression := uint32(len(entry.data))
		if entry.isDirectory {
			packedCompression |= uint32(directoryCompression) << 28
		}
		binary.LittleEndian.PutUint32(buffer[descriptorOffset:descriptorOffset+4], packedCompression)
		binary.LittleEndian.PutUint32(buffer[descriptorOffset+4:descriptorOffset+8], uint32(len(entry.data)))
		binary.LittleEndian.PutUint32(buffer[descriptorOffset+8:descriptorOffset+12], 0)
		binary.LittleEndian.PutUint32(buffer[descriptorOffset+12:descriptorOffset+16], uint32(entry.offset/16))
		copy(buffer[int(entry.offset):int(entry.offset)+len(entry.data)], entry.data)
	}

	if err := os.WriteFile(archivePath, buffer, 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildTestDirectoryTree(files map[string]string) map[string]testDirectoryEntry {
	directories := map[string]testDirectoryEntry{
		"": {subdirectories: []string{}, files: []string{}},
	}

	addSubdirectory := func(parent, subdirectory string) {
		entry := directories[parent]
		for _, existing := range entry.subdirectories {
			if existing == subdirectory {
				return
			}
		}
		entry.subdirectories = append(entry.subdirectories, subdirectory)
		directories[parent] = entry
	}
	addFile := func(parent, file string) {
		entry := directories[parent]
		for _, existing := range entry.files {
			if existing == file {
				return
			}
		}
		entry.files = append(entry.files, file)
		directories[parent] = entry
	}
	ensureDirectory := func(path string) {
		if _, ok := directories[path]; !ok {
			directories[path] = testDirectoryEntry{files: []string{}, subdirectories: []string{}}
		}
	}

	for filePath := range files {
		filePath = normalizeArchivePath(filePath)
		parts := strings.Split(filePath, "/")
		if len(parts) == 0 {
			continue
		}

		parent := ""
		for idx := 0; idx < len(parts)-1; idx++ {
			child := parts[idx]
			addSubdirectory(parent, child)
			if parent == "" {
				parent = child
			} else {
				parent = parent + "/" + child
			}
			ensureDirectory(parent)
		}

		addFile(parent, parts[len(parts)-1])
	}

	for path, entry := range directories {
		sort.Strings(entry.subdirectories)
		sort.Strings(entry.files)
		directories[path] = entry
	}
	return directories
}

func buildTestArchiveEntries(directories map[string]testDirectoryEntry, files map[string]string, v2 bool) []testArchiveEntry {
	dirPaths := make([]string, 0, len(directories))
	for directory := range directories {
		dirPaths = append(dirPaths, directory)
	}
	sort.Strings(dirPaths)

	filePaths := make([]string, 0, len(files))
	for file := range files {
		filePaths = append(filePaths, normalizeArchivePath(file))
	}
	sort.Strings(filePaths)

	entries := make([]testArchiveEntry, 0, len(dirPaths)+len(filePaths))
	for _, directory := range dirPaths {
		data := encodeDirectoryEntryData(directories[directory], v2)
		entries = append(entries, testArchiveEntry{
			path:        directory,
			data:        data,
			isDirectory: true,
		})
	}
	for _, filePath := range filePaths {
		entries = append(entries, testArchiveEntry{
			path:        filePath,
			data:        []byte(files[filePath]),
			isDirectory: false,
		})
	}
	return entries
}

func encodeDirectoryEntryData(entry testDirectoryEntry, v2 bool) []byte {
	if !v2 {
		var builder strings.Builder
		for _, subdirectory := range entry.subdirectories {
			builder.WriteString("*")
			builder.WriteString(subdirectory)
			builder.WriteString("\n")
		}
		for _, file := range entry.files {
			builder.WriteString(file)
			builder.WriteString("\n")
		}
		return []byte(builder.String())
	}

	items := make([]string, 0, len(entry.subdirectories)+len(entry.files))
	for _, subdirectory := range entry.subdirectories {
		items = append(items, "/"+subdirectory)
	}
	items = append(items, entry.files...)

	totalStringsLen := 0
	for _, item := range items {
		totalStringsLen += len(item)
	}

	buffer := make([]byte, 4+len(items)+totalStringsLen)
	binary.LittleEndian.PutUint32(buffer[:4], uint32(len(items)))
	offset := 4 + len(items)
	for idx, item := range items {
		buffer[4+idx] = byte(len(item))
		copy(buffer[offset:offset+len(item)], item)
		offset += len(item)
	}
	return buffer
}

func putUint24LE(dst []byte, value uint32) {
	dst[0] = byte(value)
	dst[1] = byte(value >> 8)
	dst[2] = byte(value >> 16)
}

func align16(value int) int {
	return (value + 15) &^ 15
}
