// Package catalog reads a normalized NESQL export without making it part of
// the live inventory database. The export is immutable input: inventory
// quantities and craftability always remain owned by the OC observations.
package catalog

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/CaeliaEve/ae-orbit/internal/nbt"
	"github.com/CaeliaEve/ae-orbit/internal/protocol"
)

const (
	manifestVersion = 1
	maxManifestSize = 128 << 20
	maxImageSize    = 2 << 20
	maxSourceFile   = 256 << 20
	maxSourceRow    = 4 << 20
	maxSourceItems  = 2_000_000
)

var (
	ErrInvalidManifest  = errors.New("invalid catalog manifest")
	ErrUnsafePath       = errors.New("unsafe catalog image path")
	ErrIncompleteSource = errors.New("incomplete NESQL++ source")
)

// Manifest is the normalized, generated view of an NESQL export. The source
// files themselves stay outside this structure and are never rewritten.
type Manifest struct {
	Version int             `json:"version"`
	Source  json.RawMessage `json:"source,omitempty"`
	Items   []Item          `json:"items"`
}

// Item is one exact, immutable catalog record. Source records are keyed by the
// NESQL++ item identity; the legacy fields remain for compatibility with
// existing API fixtures.
type Item struct {
	Key             string                  `json:"key"`
	Name            string                  `json:"name"`
	Damage          int                     `json:"damage"`
	NBT             string                  `json:"nbt,omitempty"`
	LocalizedName   string                  `json:"localized_name,omitempty"`
	InternalName    string                  `json:"internal_name,omitempty"`
	UnlocalizedName string                  `json:"unlocalized_name,omitempty"`
	ModID           string                  `json:"mod_id,omitempty"`
	MaxDamage       int                     `json:"max_damage,omitempty"`
	MaxStackSize    int                     `json:"max_stack_size,omitempty"`
	ImagePath       string                  `json:"image_path,omitempty"`
	ImageWidth      int                     `json:"width,omitempty"`
	ImageHeight     int                     `json:"height,omitempty"`
	Frames          []protocol.CatalogFrame `json:"frames,omitempty"`
	Interpolate     bool                    `json:"interpolate,omitempty"`
	Tooltips        []string                `json:"tooltips,omitempty"`
}

type assetMeta struct {
	Path        string
	Width       int
	Height      int
	Frames      []protocol.CatalogFrame
	Interpolate bool
}

type match struct {
	item  Item
	state string
}

// Catalog is a read-only in-memory index over an Elysium Source export. The
// legacy manifest/image.zip reader remains isolated for compatibility tests,
// but the configured runtime path is NESQL++ only.
type Catalog struct {
	root             string
	source           bool
	imageFile        *os.File
	zip              *zip.Reader
	byKey            map[string][]Item
	byImage          map[string]struct{}
	archiveImages    map[string]struct{}
	sourceItems      map[string]Item
	sourceResources  map[string]Item
	sourceAssets     map[string]struct{}
	sourceAssetPaths map[string]string
	sourceAssetMeta  map[string]assetMeta
	sourceTexts      map[string]map[string]string
}

// Open loads a normalized catalog directory. An empty root disables catalog
// integration and returns (nil, nil). A configured but invalid root is an
// error so deployment cannot silently serve a partial catalog.
func Open(root string) (*Catalog, error) {
	if strings.TrimSpace(root) == "" {
		return nil, nil
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(root, "manifest.json")
	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return nil, err
	}
	defer manifestFile.Close()
	manifestBytes, err := io.ReadAll(io.LimitReader(manifestFile, maxManifestSize+1))
	if err != nil {
		return nil, err
	}
	if len(manifestBytes) > maxManifestSize {
		return nil, fmt.Errorf("catalog manifest exceeds size limit")
	}
	var envelope struct {
		Format string `json:"format"`
	}
	if err = json.Unmarshal(manifestBytes, &envelope); err != nil {
		return nil, fmt.Errorf("decode catalog manifest: %w", err)
	}
	if envelope.Format == "elysium.source" {
		return openElysiumSource(root, manifestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	var manifest Manifest
	if err = decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode catalog manifest: %w", err)
	}
	if manifest.Version != manifestVersion || len(manifest.Items) == 0 {
		return nil, ErrInvalidManifest
	}
	if err = validateItems(manifest.Items); err != nil {
		return nil, err
	}
	catalog := &Catalog{root: root, byKey: make(map[string][]Item, len(manifest.Items)), byImage: map[string]struct{}{}, archiveImages: map[string]struct{}{}}
	for _, item := range manifest.Items {
		key := item.Key
		if key == "" {
			key = baseKey(item.Name, item.Damage)
		}
		catalog.byKey[key] = append(catalog.byKey[key], item)
		if item.ImagePath != "" {
			catalog.byImage[item.ImagePath] = struct{}{}
		}
	}
	imageFile, err := os.Open(filepath.Join(root, "image.zip"))
	if err != nil {
		// Metadata-only catalogs are useful during an import. Keep the catalog
		// usable and report images as unavailable until the archive is installed.
		return catalog, nil
	}
	info, err := imageFile.Stat()
	if err != nil {
		imageFile.Close()
		return nil, err
	}
	archive, err := zip.NewReader(imageFile, info.Size())
	if err != nil {
		imageFile.Close()
		return nil, fmt.Errorf("open catalog image archive: %w", err)
	}
	for _, entry := range archive.File {
		name := strings.TrimSuffix(strings.ReplaceAll(entry.Name, "\\", "/"), "/")
		if name == "" {
			continue
		}
		if safe, pathErr := safeImagePath(name); pathErr == nil {
			if _, registered := catalog.byImage[safe]; registered {
				catalog.archiveImages[safe] = struct{}{}
			}
		}
	}
	catalog.imageFile, catalog.zip = imageFile, archive
	return catalog, nil
}

// OpenNESQLPP is the production catalog entry point. It deliberately rejects
// the legacy normalized NESQL manifest so a deployment cannot silently fall
// back to stale metadata or image.zip assets.
func OpenNESQLPP(root string) (*Catalog, error) {
	catalog, err := Open(root)
	if err != nil || catalog == nil {
		return catalog, err
	}
	if !catalog.source {
		_ = catalog.Close()
		return nil, ErrIncompleteSource
	}
	return catalog, nil
}

// PlainCandidates returns exact untagged identities from the immutable NESQL
// source. It supplies query candidates, never inventory observations.
func (c *Catalog) PlainCandidates() []protocol.Definition {
	if c == nil || !c.source {
		return nil
	}
	items := make([]protocol.Definition, 0, len(c.sourceItems))
	seen := map[string]bool{}
	for _, item := range c.sourceItems {
		if item.NBT != "" {
			continue
		}
		d := protocol.Definition{Kind: "item", Name: item.Name, Damage: item.Damage, Label: item.LocalizedName, Exact: true}
		if d.Label == "" {
			d.Label = d.Name
		}
		if d.Validate() != nil || seen[d.Key()] {
			continue
		}
		seen[d.Key()] = true
		items = append(items, d)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].Damage < items[j].Damage
		}
		return items[i].Name < items[j].Name
	})
	return items
}

func openElysiumSource(root string, manifestBytes []byte) (*Catalog, error) {
	var manifest struct {
		Format   string `json:"format"`
		Revision int    `json:"revision"`
		State    string `json:"state"`
		Scope    struct {
			Mode        string   `json:"mode"`
			Collections []string `json:"collections"`
		} `json:"scope"`
		Files []struct {
			Path     string `json:"path"`
			Kind     string `json:"kind"`
			Encoding string `json:"encoding"`
		} `json:"files"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("decode NESQL++ source manifest: %w", err)
	}
	if manifest.Format != "elysium.source" || manifest.Revision < 41 ||
		manifest.State != "" && manifest.State != "complete" ||
		manifest.Scope.Mode != "complete" && manifest.Scope.Mode != "selection" {
		return nil, ErrIncompleteSource
	}
	imageSelection := manifest.Scope.Mode == "selection"
	collections := make(map[string]struct{}, len(manifest.Scope.Collections))
	for _, collection := range manifest.Scope.Collections {
		collections[collection] = struct{}{}
	}
	requiredCollections := []string{"assets", "items"}
	if imageSelection {
		// A selection export is admitted only as an immutable image catalog.
		// Strings are required so the UI can resolve names/tooltips without
		// reaching for a second, potentially mismatched source.
		requiredCollections = append(requiredCollections, "strings")
	}
	for _, required := range requiredCollections {
		if _, ok := collections[required]; !ok {
			return nil, ErrIncompleteSource
		}
	}
	source := &Catalog{
		root:             root,
		source:           true,
		sourceItems:      make(map[string]Item),
		sourceResources:  make(map[string]Item),
		sourceAssets:     make(map[string]struct{}),
		sourceAssetPaths: make(map[string]string),
		sourceAssetMeta:  make(map[string]assetMeta),
		sourceTexts:      make(map[string]map[string]string),
	}
	var itemFiles, assetRecordFiles, stringFiles, fluidFiles, aspectFiles []string
	for _, file := range manifest.Files {
		path, err := safeSourcePath(file.Path)
		if err != nil {
			return nil, err
		}
		switch file.Kind {
		case "items":
			itemFiles = append(itemFiles, path)
		case "fluids":
			fluidFiles = append(fluidFiles, path)
		case "aspects":
			aspectFiles = append(aspectFiles, path)
		case "assets":
			if strings.HasPrefix(path, "records/") {
				assetRecordFiles = append(assetRecordFiles, path)
			} else if strings.HasPrefix(path, "assets/") {
				source.sourceAssets[path] = struct{}{}
			}
		case "asset":
			if strings.HasPrefix(path, "assets/") {
				source.sourceAssets[path] = struct{}{}
			}
		case "strings":
			stringFiles = append(stringFiles, path)
		}
	}
	if len(itemFiles) == 0 || len(assetRecordFiles) == 0 || imageSelection && len(stringFiles) == 0 {
		return nil, ErrIncompleteSource
	}
	if err := readSourceRows(root, stringFiles, func(row map[string]json.RawMessage) error {
		id, _ := rawString(row["id"])
		locale, _ := rawString(row["locale"])
		text, textOK := rawString(row["text"])
		if id == "" || locale == "" || !textOK {
			return fmt.Errorf("invalid NESQL++ string record")
		}
		locales := source.sourceTexts[id]
		if locales == nil {
			locales = make(map[string]string)
			source.sourceTexts[id] = locales
		}
		if prior, exists := locales[locale]; exists && prior != text {
			return fmt.Errorf("conflicting NESQL++ string record %s/%s", id, locale)
		}
		locales[locale] = text
		return nil
	}); err != nil {
		return nil, err
	}
	if err := readSourceRows(root, assetRecordFiles, func(row map[string]json.RawMessage) error {
		id, _ := rawString(row["id"])
		path, _ := rawString(row["path"])
		if path == "" {
			path, _ = rawString(row["asset"])
		}
		if id == "" || path == "" {
			return fmt.Errorf("invalid NESQL++ asset record")
		}
		path, err := safeSourceAssetPath(path)
		if err != nil {
			return err
		}
		if prior, exists := source.sourceAssetPaths[id]; exists && prior != path {
			return fmt.Errorf("conflicting NESQL++ asset record %s", id)
		}
		meta, err := parseAssetMeta(row, path)
		if err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			return fmt.Errorf("%w: missing NESQL++ asset file %s", ErrIncompleteSource, path)
		}
		if prior, exists := source.sourceAssetMeta[id]; exists && !sameAssetMeta(prior, meta) {
			return fmt.Errorf("conflicting NESQL++ asset metadata %s", id)
		}
		source.sourceAssets[id] = struct{}{}
		source.sourceAssets[path] = struct{}{}
		source.sourceAssetPaths[id] = path
		source.sourceAssetMeta[id] = meta
		source.sourceAssetMeta[path] = meta
		return nil
	}); err != nil {
		return nil, err
	}
	if err := readSourceRows(root, itemFiles, func(row map[string]json.RawMessage) error {
		id, _ := rawString(row["id"])
		registry, _ := rawString(row["registry"])
		meta, err := rawInt(row["meta"])
		if err != nil {
			return fmt.Errorf("invalid NESQL++ item meta: %w", err)
		}
		if id == "" || registry == "" || meta < 0 {
			return fmt.Errorf("invalid NESQL++ item record")
		}
		nameID, _ := rawString(row["name"])
		item := Item{
			Key:             id,
			Name:            registry,
			Damage:          meta,
			InternalName:    registry,
			UnlocalizedName: registry,
			ModID:           modID(registry),
			MaxDamage:       rawIntDefault(row["durability"]),
			MaxStackSize:    rawIntDefault(row["stackLimit"]),
		}
		if rawNBT := bytes.TrimSpace(row["nbt"]); len(rawNBT) > 0 && !bytes.Equal(rawNBT, []byte("null")) {
			item.NBT = string(rawNBT)
		}
		item.LocalizedName = source.resolveText(nameID)
		if item.LocalizedName == "" {
			item.LocalizedName = registry
		}
		if tooltip, ok := row["tooltip"]; ok {
			var values []json.RawMessage
			if err := json.Unmarshal(tooltip, &values); err != nil {
				return fmt.Errorf("invalid NESQL++ item tooltip: %w", err)
			}
			for _, value := range values {
				textID, _ := rawString(value)
				if textID == "" {
					continue
				}
				if resolved := source.resolveText(textID); resolved != "" {
					item.Tooltips = append(item.Tooltips, resolved)
				} else {
					item.Tooltips = append(item.Tooltips, textID)
				}
			}
		}
		icon, _ := rawString(row["icon"])
		if icon != "" {
			if path := source.assetPath(icon); path != "" {
				item.ImagePath = path
				if meta, ok := source.sourceAssetMeta[icon]; ok {
					item.ImageWidth = meta.Width
					item.ImageHeight = meta.Height
					item.Frames = cloneFrames(meta.Frames)
					item.Interpolate = meta.Interpolate
				} else if meta, ok := source.sourceAssetMeta[path]; ok {
					item.ImageWidth = meta.Width
					item.ImageHeight = meta.Height
					item.Frames = cloneFrames(meta.Frames)
					item.Interpolate = meta.Interpolate
				}
			} else if imageSelection {
				return fmt.Errorf("%w: unresolved NESQL++ item icon %s", ErrIncompleteSource, icon)
			}
		} else if imageSelection {
			return fmt.Errorf("%w: NESQL++ item %s has no icon", ErrIncompleteSource, id)
		}
		if _, exists := source.sourceItems[id]; exists {
			return fmt.Errorf("duplicate NESQL++ item id %s", id)
		}
		source.sourceItems[id] = item
		if len(source.sourceItems) > maxSourceItems {
			return fmt.Errorf("NESQL++ item catalog exceeds limit")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if len(source.sourceItems) == 0 {
		return nil, ErrIncompleteSource
	}
	if err := source.readResources(fluidFiles, "fluid"); err != nil {
		return nil, err
	}
	if err := source.readResources(aspectFiles, "essentia"); err != nil {
		return nil, err
	}
	return source, nil
}

func readSourceRows(root string, paths []string, consume func(map[string]json.RawMessage) error) error {
	for _, relative := range paths {
		file, err := os.Open(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return fmt.Errorf("open NESQL++ source file %s: %w", relative, err)
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return err
		}
		if info.Size() <= 0 || info.Size() > maxSourceFile {
			file.Close()
			return fmt.Errorf("NESQL++ source file exceeds limit: %s", relative)
		}
		reader, err := gzip.NewReader(file)
		if err != nil {
			file.Close()
			return fmt.Errorf("open NESQL++ gzip file %s: %w", relative, err)
		}
		reader.Multistream(false)
		scanner := bufio.NewScanner(io.LimitReader(reader, maxSourceFile+1))
		scanner.Buffer(make([]byte, 64<<10), maxSourceRow)
		line := 0
		for scanner.Scan() {
			line++
			data := bytes.TrimSpace(scanner.Bytes())
			if len(data) == 0 {
				continue
			}
			var row map[string]json.RawMessage
			if err := json.Unmarshal(data, &row); err != nil {
				reader.Close()
				file.Close()
				return fmt.Errorf("decode NESQL++ source row %s:%d: %w", relative, line, err)
			}
			if err := consume(row); err != nil {
				reader.Close()
				file.Close()
				return fmt.Errorf("invalid NESQL++ source row %s:%d: %w", relative, line, err)
			}
		}
		scanErr := scanner.Err()
		closeErr := reader.Close()
		file.Close()
		if scanErr != nil {
			return fmt.Errorf("read NESQL++ source file %s: %w", relative, scanErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close NESQL++ source file %s: %w", relative, closeErr)
		}
	}
	return nil
}

func rawString(value json.RawMessage) (string, bool) {
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return "", false
	}
	var result string
	if json.Unmarshal(value, &result) != nil {
		return "", false
	}
	return result, true
}

func rawInt(value json.RawMessage) (int, error) {
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return 0, nil
	}
	var number int
	if err := json.Unmarshal(value, &number); err == nil {
		return number, nil
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return 0, err
	}
	number, err := strconv.Atoi(text)
	if err != nil {
		return 0, err
	}
	return number, nil
}

func rawIntDefault(value json.RawMessage) int {
	number, err := rawInt(value)
	if err != nil || number < 0 {
		return 0
	}
	return number
}

func parseAssetMeta(row map[string]json.RawMessage, path string) (assetMeta, error) {
	meta := assetMeta{Path: path}
	var err error
	meta.Width, err = rawInt(row["width"])
	if err != nil || meta.Width < 0 || meta.Width > 16384 {
		return assetMeta{}, fmt.Errorf("invalid NESQL++ asset width")
	}
	meta.Height, err = rawInt(row["height"])
	if err != nil || meta.Height < 0 || meta.Height > 16384 {
		return assetMeta{}, fmt.Errorf("invalid NESQL++ asset height")
	}
	rawFrames := bytes.TrimSpace(row["frames"])
	if len(rawFrames) > 0 && !bytes.Equal(rawFrames, []byte("null")) {
		var frames []protocol.CatalogFrame
		if err := json.Unmarshal(rawFrames, &frames); err != nil {
			return assetMeta{}, fmt.Errorf("invalid NESQL++ asset frames: %w", err)
		}
		if len(frames) > 4096 {
			return assetMeta{}, fmt.Errorf("NESQL++ asset timeline exceeds limit")
		}
		for _, frame := range frames {
			if frame.X < 0 || frame.Y < 0 || frame.Width < 1 || frame.Height < 1 || frame.Ticks < 1 || frame.Ticks > 72000 {
				return assetMeta{}, fmt.Errorf("invalid NESQL++ asset frame")
			}
		}
		if meta.Width < 1 || meta.Height < 1 {
			return assetMeta{}, fmt.Errorf("animated NESQL++ asset has invalid dimensions")
		}
		meta.Frames = frames
	}
	if rawInterpolate := bytes.TrimSpace(row["interpolate"]); len(rawInterpolate) > 0 && !bytes.Equal(rawInterpolate, []byte("null")) {
		if err := json.Unmarshal(rawInterpolate, &meta.Interpolate); err != nil {
			return assetMeta{}, fmt.Errorf("invalid NESQL++ asset interpolation flag: %w", err)
		}
	}
	return meta, nil
}

func cloneFrames(frames []protocol.CatalogFrame) []protocol.CatalogFrame {
	if len(frames) == 0 {
		return nil
	}
	return append([]protocol.CatalogFrame(nil), frames...)
}

func sameAssetMeta(left, right assetMeta) bool {
	if left.Path != right.Path || left.Width != right.Width || left.Height != right.Height ||
		left.Interpolate != right.Interpolate || len(left.Frames) != len(right.Frames) {
		return false
	}
	for index := range left.Frames {
		if left.Frames[index] != right.Frames[index] {
			return false
		}
	}
	return true
}

func modID(registry string) string {
	if index := strings.IndexByte(registry, ':'); index > 0 {
		return registry[:index]
	}
	return ""
}

func safeSourcePath(value string) (string, error) {
	value = strings.ReplaceAll(value, "\\", "/")
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\x00") {
		return "", ErrUnsafePath
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || strings.Contains(clean, ":") {
		return "", ErrUnsafePath
	}
	return clean, nil
}

func safeSourceAssetPath(value string) (string, error) {
	clean, err := safeSourcePath(value)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(clean, "assets/") || filepath.Ext(clean) != ".png" {
		return "", ErrUnsafePath
	}
	return clean, nil
}

func (c *Catalog) resolveText(id string) string {
	if id == "" {
		return ""
	}
	locales := c.sourceTexts[id]
	if len(locales) == 0 {
		return ""
	}
	if value := locales["en_US"]; value != "" {
		return value
	}
	for _, value := range locales {
		return value
	}
	return ""
}

func (c *Catalog) assetPath(icon string) string {
	if path := c.sourceAssetPaths[icon]; path != "" {
		if _, err := os.Stat(filepath.Join(c.root, filepath.FromSlash(path))); err == nil {
			return path
		}
	}
	if path, err := safeSourceAssetPath(icon); err == nil {
		// Accept a path-shaped icon only when it also has a parsed asset
		// record. This keeps selection sources from bypassing asset metadata.
		if _, registered := c.sourceAssetMeta[path]; registered {
			if _, err := os.Stat(filepath.Join(c.root, filepath.FromSlash(path))); err == nil {
				return path
			}
		}
	}
	return ""
}

func validateItems(items []Item) error {
	for _, item := range items {
		if item.Name == "" || item.Damage < 0 || item.Damage > 2147483647 {
			return ErrInvalidManifest
		}
		if item.Key != "" && item.Key != baseKey(item.Name, item.Damage) {
			return ErrInvalidManifest
		}
		if item.ImagePath != "" {
			if _, err := safeImagePath(item.ImagePath); err != nil {
				return err
			}
		}
	}
	return nil
}

func baseKey(name string, damage int) string { return fmt.Sprintf("%s@%d", name, damage) }

func safeImagePath(value string) (string, error) {
	value = strings.ReplaceAll(value, "\\", "/")
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\x00") {
		return "", ErrUnsafePath
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || strings.Contains(clean, ":") {
		return "", ErrUnsafePath
	}
	for _, segment := range strings.Split(clean, "/") {
		if segment == ".." || segment == "." {
			return "", ErrUnsafePath
		}
	}
	if !strings.HasPrefix(clean, "item/") {
		return "", ErrUnsafePath
	}
	return clean, nil
}

// Lookup returns a catalog match only when the identity is unambiguous. NBT
// variants are deliberately not guessed because exporters may use a different
// canonical representation than the OC wire protocol.
func (c *Catalog) Lookup(name string, damage int, nbt string) (Item, string, bool) {
	if c == nil || name == "" {
		return Item{}, "unmatched", false
	}
	if c.source {
		var compressed []byte
		if nbt != "" {
			decoded, err := base64.StdEncoding.Strict().DecodeString(nbt)
			if err != nil {
				return Item{}, "unmatched", false
			}
			compressed = decoded
		}
		id, err := nbtid(name, damage, compressed)
		if err != nil {
			return Item{}, "unmatched", false
		}
		item, found := c.sourceItems[id]
		if !found {
			return Item{}, "unmatched", false
		}
		if item.ImagePath == "" {
			return item, "missing", true
		}
		if _, err := os.Stat(filepath.Join(c.root, filepath.FromSlash(item.ImagePath))); err != nil {
			return item, "missing", true
		}
		return item, "available", true
	}
	if nbt != "" {
		return Item{}, "unmatched", false
	}
	items := c.byKey[baseKey(name, damage)]
	plain := make([]Item, 0, len(items))
	for _, item := range items {
		if item.NBT == "" {
			plain = append(plain, item)
		}
	}
	if len(plain) == 1 {
		items = plain
	} else if len(plain) > 1 {
		return Item{}, "ambiguous", false
	} else {
		return Item{}, "unmatched", false
	}
	item := items[0]
	if item.ImagePath == "" || c.archiveImages == nil {
		return item, "missing", true
	}
	if _, found := c.archiveImages[item.ImagePath]; !found {
		return item, "missing", true
	}
	return item, "available", true
}

func nbtid(name string, damage int, compressed []byte) (string, error) {
	return nbt.NESQLItemID(name, damage, compressed)
}

// Public builds the stable API representation. ImageURL is filled by the API
// layer because it includes the current network and item identity.
func (item Item) Public(state string) protocol.CatalogItem {
	return protocol.CatalogItem{
		LocalizedName:   item.LocalizedName,
		InternalName:    item.InternalName,
		UnlocalizedName: item.UnlocalizedName,
		ModID:           item.ModID,
		MaxDamage:       item.MaxDamage,
		MaxStackSize:    item.MaxStackSize,
		ImageKey:        item.ImagePath,
		ImageState:      state,
		Width:           item.ImageWidth,
		Height:          item.ImageHeight,
		Frames:          cloneFrames(item.Frames),
		Interpolate:     item.Interpolate,
		Tooltips:        append([]string(nil), item.Tooltips...),
	}
}

// Image reads an archive member after validating that it is registered in the
// manifest. The returned bytes are bounded to prevent a malformed archive from
// becoming an unbounded response.
func (c *Catalog) Image(item Item) ([]byte, string, error) {
	if c != nil && c.source {
		if item.ImagePath == "" {
			return nil, "", os.ErrNotExist
		}
		path, err := safeSourceAssetPath(item.ImagePath)
		if err != nil {
			return nil, "", err
		}
		if _, registered := c.sourceAssets[path]; !registered {
			return nil, "", ErrUnsafePath
		}
		file, err := os.Open(filepath.Join(c.root, filepath.FromSlash(path)))
		if err != nil {
			return nil, "", err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return nil, "", err
		}
		if info.Size() > maxImageSize {
			return nil, "", fmt.Errorf("catalog image exceeds size limit")
		}
		data, err := io.ReadAll(io.LimitReader(file, maxImageSize+1))
		if err != nil {
			return nil, "", err
		}
		if len(data) > maxImageSize {
			return nil, "", fmt.Errorf("catalog image exceeds size limit")
		}
		digest := sha256.Sum256(data)
		return data, hex.EncodeToString(digest[:]), nil
	}
	if c == nil || c.zip == nil || item.ImagePath == "" {
		return nil, "", os.ErrNotExist
	}
	path, err := safeImagePath(item.ImagePath)
	if err != nil {
		return nil, "", ErrUnsafePath
	}
	if _, registered := c.byImage[path]; !registered {
		return nil, "", ErrUnsafePath
	}
	if _, found := c.archiveImages[path]; !found {
		return nil, "", ErrUnsafePath
	}
	for _, entry := range c.zip.File {
		name := strings.TrimSuffix(strings.ReplaceAll(entry.Name, "\\", "/"), "/")
		if name != path {
			continue
		}
		if entry.UncompressedSize64 > maxImageSize {
			return nil, "", fmt.Errorf("catalog image exceeds size limit")
		}
		r, err := entry.Open()
		if err != nil {
			return nil, "", err
		}
		data, readErr := io.ReadAll(io.LimitReader(r, maxImageSize+1))
		r.Close()
		if readErr != nil {
			return nil, "", readErr
		}
		if len(data) > maxImageSize {
			return nil, "", fmt.Errorf("catalog image exceeds size limit")
		}
		digest := sha256.Sum256(data)
		return data, hex.EncodeToString(digest[:]), nil
	}
	return nil, "", os.ErrNotExist
}

func (c *Catalog) Close() error {
	if c == nil || c.imageFile == nil {
		return nil
	}
	return c.imageFile.Close()
}

// ImageURLPath validates an item id before it is embedded in a URL path.
func ImageURLPath(network string, item protocol.Quantity) string {
	return "/api/v1/networks/" + url.PathEscape(network) + "/catalog/items/" + item.String() + "/image"
}
