package folder

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/simulot/immich-go/adapters"
	"github.com/simulot/immich-go/helpers/gen"
	"github.com/simulot/immich-go/internal/fileevent"
	"github.com/simulot/immich-go/internal/fshelper"
	"github.com/simulot/immich-go/internal/metadata"
)

type fileLinks struct {
	image   string
	video   string
	sidecar string
}

type LocalAssetBrowser struct {
	fsyss    []fs.FS
	albums   map[string]string
	catalogs map[fs.FS]map[string][]string
	log      *fileevent.Recorder
	flags    *ImportFlags
	exiftool *metadata.ExifTool
}

func NewLocalFiles(ctx context.Context, l *fileevent.Recorder, flags *ImportFlags, fsyss ...fs.FS) (*LocalAssetBrowser, error) {
	if flags.ImportIntoAlbum != "" && flags.UsePathAsAlbumName != FolderModeNone {
		return nil, errors.New("cannot use both --into-album and --folder-as-album")
	}

	la := LocalAssetBrowser{
		fsyss:    fsyss,
		albums:   map[string]string{},
		catalogs: map[fs.FS]map[string][]string{},
		flags:    flags,
		log:      l,
	}

	if flags.ExifToolFlags.UseExifTool {
		et, err := metadata.NewExifTool(&flags.ExifToolFlags)
		if err != nil {
			return nil, err
		}
		la.exiftool = et
	}

	return &la, nil
}

func (la *LocalAssetBrowser) Browse(ctx context.Context) (chan *adapters.AssetGroup, error) {
	for _, fsys := range la.fsyss {
		err := la.passOneFsWalk(ctx, fsys)
		if err != nil {
			return nil, err
		}
	}
	return la.passTwo(ctx), nil
}

func (la *LocalAssetBrowser) passOneFsWalk(ctx context.Context, fsys fs.FS) error {
	la.catalogs[fsys] = map[string][]string{}
	err := fs.WalkDir(fsys, ".",
		func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				if !la.flags.Recursive && name != "." {
					return fs.SkipDir
				}
				la.catalogs[fsys][name] = []string{}
				return nil
			}
			select {
			case <-ctx.Done():
				// If the context has been cancelled, return immediately
				return ctx.Err()
			default:
				if la.flags.BannedFiles.Match(name) {
					la.log.Record(ctx, fileevent.DiscoveredDiscarded, fileevent.AsFileAndName(fsys, name), "reason", "banned file")
					return nil
				}

				dir, base := filepath.Split(name)
				dir = strings.TrimSuffix(dir, "/")
				if dir == "" {
					dir = "."
				}
				ext := filepath.Ext(base)
				mediaType := la.flags.SupportedMedia.TypeFromExt(ext)

				if mediaType == metadata.TypeUnknown {
					la.log.Record(ctx, fileevent.DiscoveredUnsupported, fileevent.AsFileAndName(fsys, name), "reason", "unsupported file type")
					return nil
				}

				cat := la.catalogs[fsys][dir]

				switch mediaType {
				case metadata.TypeImage:
					la.log.Record(ctx, fileevent.DiscoveredImage, fileevent.AsFileAndName(fsys, name))
				case metadata.TypeVideo:
					la.log.Record(ctx, fileevent.DiscoveredVideo, fileevent.AsFileAndName(fsys, name))
				case metadata.TypeSidecar:
					la.log.Record(ctx, fileevent.DiscoveredSidecar, fileevent.AsFileAndName(fsys, name))
					if la.flags.IgnoreSideCarFiles {
						la.log.Record(ctx, fileevent.DiscoveredDiscarded, fileevent.AsFileAndName(fsys, name), "reason", "sidecar file ignored")
						return nil
					}
				}

				if !la.flags.InclusionFlags.IncludedExtensions.Include(ext) {
					la.log.Record(ctx, fileevent.DiscoveredDiscarded, fileevent.AsFileAndName(fsys, name), "reason", "extension not included")
					return nil
				}

				if la.flags.InclusionFlags.ExcludedExtensions.Exclude(ext) {
					la.log.Record(ctx, fileevent.DiscoveredDiscarded, fileevent.AsFileAndName(fsys, name), "reason", "extension excluded")
					return nil
				}

				la.catalogs[fsys][dir] = append(cat, name)
			}
			return nil
		})
	return err
}

func (la *LocalAssetBrowser) passTwo(ctx context.Context) chan *adapters.AssetGroup {
	fileChan := make(chan *adapters.AssetGroup)
	// Browse all given FS to collect the list of files
	go func(ctx context.Context) {
		defer close(fileChan)
		var err error
		if la.exiftool != nil {
			defer la.exiftool.Close()
		}

		errFn := func(name fileevent.FileAndName, err error) {
			if err != nil {
				la.log.Record(ctx, fileevent.Error, name, "error", err.Error())
			}
		}
		for _, fsys := range la.fsyss {
			dirs := gen.MapKeys(la.catalogs[fsys])
			sort.Strings(dirs)
			for _, dir := range dirs {
				links := map[string]fileLinks{}
				files := la.catalogs[fsys][dir]

				if len(files) == 0 {
					continue
				}

				// Scan images first
				for _, file := range files {
					ext := path.Ext(file)
					if la.flags.SupportedMedia.TypeFromExt(ext) == metadata.TypeImage {
						linked := links[file]
						linked.image = file
						links[file] = linked
					}
				}

			next:
				for _, file := range files {
					ext := path.Ext(file)
					t := la.flags.SupportedMedia.TypeFromExt(ext)
					if t == metadata.TypeImage {
						continue next
					}

					base := strings.TrimSuffix(file, ext)
					switch t {
					case metadata.TypeSidecar:
						if image, ok := links[base]; ok {
							// file.ext.XMP -> file.ext
							image.sidecar = file
							links[base] = image
							continue next
						}
						for f := range links {
							if strings.TrimSuffix(f, path.Ext(f)) == base {
								if image, ok := links[f]; ok {
									// base.XMP -> base.ext
									image.sidecar = file
									links[f] = image
									continue next
								}
							}
						}
					case metadata.TypeVideo:
						if image, ok := links[base]; ok {
							// file.MP.ext -> file.ext
							image.sidecar = file
							links[base] = image
							continue next
						}
						for f := range links {
							if strings.TrimSuffix(f, path.Ext(f)) == base {
								if image, ok := links[f]; ok {
									// base.MP4 -> base.ext
									image.video = file
									links[f] = image
									continue next
								}
							}
							if strings.TrimSuffix(f, path.Ext(f)) == file {
								if image, ok := links[f]; ok {
									// base.ext -> base
									image.video = file
									links[f] = image
									continue next
								}
							}
						}
						// Unlinked video
						links[file] = fileLinks{video: file}
					}
				}

				files = gen.MapKeys(links)
				sort.Strings(files)
				for _, file := range files {
					var a *adapters.LocalAssetFile
					var g *adapters.AssetGroup
					linked := links[file]

					switch {
					case linked.image != "" && linked.video != "":
						a, err = la.assetFromFile(ctx, fsys, linked.image)
						if err != nil {
							errFn(fileevent.AsFileAndName(fsys, linked.image), err)
							return
						}
						if a == nil {
							continue
						}
						i, err := la.assetFromFile(ctx, fsys, linked.video)
						if i != nil {
							g = &adapters.AssetGroup{
								Kind:       adapters.GroupKindMotionPhoto,
								Assets:     []*adapters.LocalAssetFile{a, i},
								CoverIndex: 0,
							}
						} else {
							errFn(fileevent.AsFileAndName(fsys, linked.video), err)
							g = &adapters.AssetGroup{
								Kind:   adapters.GroupKindNone,
								Assets: []*adapters.LocalAssetFile{a},
							}
						}
					case linked.image != "":
						a, err = la.assetFromFile(ctx, fsys, linked.image)
						if err != nil {
							errFn(fileevent.AsFileAndName(fsys, linked.image), err)
							return
						}
						if a == nil {
							continue
						}
						g = &adapters.AssetGroup{
							Kind:       adapters.GroupKindNone,
							Assets:     []*adapters.LocalAssetFile{a},
							CoverIndex: 0,
						}
					case linked.video != "":
						{
							a, err = la.assetFromFile(ctx, fsys, linked.video)
							if err != nil {
								errFn(fileevent.AsFileAndName(fsys, linked.video), err)
								return
							}
							if a == nil {
								continue
							}

							g = &adapters.AssetGroup{
								Kind:       adapters.GroupKindNone,
								Assets:     []*adapters.LocalAssetFile{a},
								CoverIndex: 0,
							}

						}
					}

					if g == nil {
						continue
					}

					if linked.sidecar != "" {
						g.SideCar = metadata.SideCarFile{
							FSys:     fsys,
							FileName: linked.sidecar,
						}
						la.log.Record(ctx, fileevent.AnalysisAssociatedMetadata, fileevent.AsFileAndName(fsys, a.FileName), "sidecar", linked.sidecar)
					}

					// manage album options
					if la.flags.ImportIntoAlbum != "" {
						g.Albums = append(g.Albums, &adapters.LocalAlbum{
							Path:  a.FileName,
							Title: la.flags.ImportIntoAlbum,
						})
					} else if la.flags.UsePathAsAlbumName != FolderModeNone {
						switch la.flags.UsePathAsAlbumName {
						case FolderModeFolder:
							title := filepath.Base(filepath.Dir(a.FileName))
							if title == "." {
								if fsys, ok := fsys.(fshelper.NameFS); ok {
									title = fsys.Name()
								}
							}
							if title != "" {
								g.Albums = append(g.Albums, &adapters.LocalAlbum{
									Path:  a.FileName,
									Title: title,
								})
							}
						case FolderModePath:
							parts := []string{}
							if fsys, ok := fsys.(fshelper.NameFS); ok {
								parts = append(parts, fsys.Name())
							}
							path := filepath.Dir(a.FileName)
							if path != "." {
								parts = append(parts, strings.Split(path, "/")...) // TODO: Check on windows
							}
							Title := strings.Join(parts, la.flags.AlbumNamePathSeparator)
							g.Albums = append(g.Albums, &adapters.LocalAlbum{
								Path:  filepath.Dir(a.FileName),
								Title: Title,
							})
						}
					}

					select {
					case <-ctx.Done():
						return
					default:
						fileChan <- g
					}
				}
			}
		}
	}(ctx)

	return fileChan
}

func (la *LocalAssetBrowser) assetFromFile(ctx context.Context, fsys fs.FS, name string) (*adapters.LocalAssetFile, error) {
	a := &adapters.LocalAssetFile{
		FileName: name,
		Title:    filepath.Base(name),
		FSys:     fsys,
	}

	err := a.ReadMetadata(la.flags.DateHandlingFlags.Method, adapters.ReadMetadataOptions{
		ExifTool:         la.exiftool,
		ExiftoolTimezone: la.flags.ExifToolFlags.Timezone.Location(),
		FilenameTimeZone: la.flags.DateHandlingFlags.FilenameTimeZone.Location(),
	})
	if err != nil {
		a.Close()
		return nil, err
	}

	i, err := fs.Stat(fsys, name)
	if err != nil {
		a.Close()
		return nil, err
	}
	a.FileSize = int(i.Size())

	if la.flags.InclusionFlags.DateRange.IsSet() && !la.flags.InclusionFlags.DateRange.InRange(a.Metadata.DateTaken) {
		a.Close()
		la.log.Record(ctx, fileevent.DiscoveredDiscarded, fileevent.AsFileAndName(fsys, name), "reason", "asset outside date range")
		return nil, nil
	}
	return a, nil
}