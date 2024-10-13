package folder

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/kr/pretty"
	"github.com/psanford/memfs"
	"github.com/simulot/immich-go/adapters"
	"github.com/simulot/immich-go/commands/application"
	"github.com/simulot/immich-go/helpers/configuration"
	cliflags "github.com/simulot/immich-go/internal/cliFlags"
	"github.com/simulot/immich-go/internal/fileevent"
	"github.com/simulot/immich-go/internal/metadata"
	"github.com/simulot/immich-go/internal/namematcher"
	"github.com/simulot/immich-go/internal/tzone"
)

type inMemFS struct {
	*memfs.FS
	name string
	err  error
}

func newInMemFS(name string) *inMemFS {
	return &inMemFS{
		name: name,
		FS:   memfs.New(),
	}
}

func (mfs inMemFS) Name() string {
	return mfs.name
}

func (mfs *inMemFS) addFile(name string) *inMemFS {
	if mfs.err != nil {
		return mfs
	}
	dir := path.Dir(name)
	mfs.err = errors.Join(mfs.err, mfs.MkdirAll(dir, 0o777))
	mfs.err = errors.Join(mfs.err, mfs.WriteFile(name, []byte(name), 0o777))
	return mfs
}

func TestInMemLocalAssets(t *testing.T) {
	tc := []struct {
		name           string
		fsys           []fs.FS
		flags          ImportFolderOptions
		expectedFiles  map[string]fileLinks
		expectedCounts []int64
		expectedAlbums map[string][]string
	}{
		// {
		// 	name: "easy",
		// 	flags: ImportFolderOptions{
		// 		SupportedMedia: metadata.DefaultSupportedMedia,
		// 	},
		// 	fsys: []fs.FS{
		// 		newInMemFS("MemFS").
		// 			addFile("root_01.jpg"),
		// 	},
		// 	expectedFiles: map[string]fileLinks{
		// 		"root_01.jpg": {image: "root_01.jpg"},
		// 	},
		// 	expectedCounts: fileevent.NewCounts().Set(fileevent.DiscoveredImage, 1).Value(),
		// },
		// {
		// 	name: "recursive",
		// 	flags: ImportFolderOptions{
		// 		SupportedMedia: metadata.DefaultSupportedMedia,
		// 		Recursive:      true,
		// 	},
		// 	fsys: []fs.FS{
		// 		newInMemFS("MemFS").
		// 			addFile("root_01.jpg").
		// 			addFile("photos/photo_01.jpg"),
		// 	},
		// 	expectedFiles: map[string]fileLinks{
		// 		"root_01.jpg":         {image: "root_01.jpg"},
		// 		"photos/photo_01.jpg": {image: "photos/photo_01.jpg"},
		// 	},
		// 	expectedCounts: fileevent.NewCounts().Set(fileevent.DiscoveredImage, 2).Value(),
		// },
		// {
		// 	name: "non-recursive",
		// 	flags: ImportFolderOptions{
		// 		SupportedMedia: metadata.DefaultSupportedMedia,
		// 		Recursive:      false,
		// 	},
		// 	fsys: []fs.FS{
		// 		newInMemFS("MemFS").
		// 			addFile("root_01.jpg").
		// 			addFile("photos/photo_01.jpg"),
		// 	},
		// 	expectedFiles: map[string]fileLinks{
		// 		"root_01.jpg": {image: "root_01.jpg"},
		// 	},
		// 	expectedCounts: fileevent.NewCounts().Set(fileevent.DiscoveredImage, 1).Value(),
		// },
		{
			name: "banned files",
			flags: ImportFolderOptions{
				BannedFiles:    namematcher.MustList(`@eaDir`, `.@__thumb`, `SYNOFILE_THUMB_*.*`, "BLOG/", "Database/", `._*.*`),
				SupportedMedia: metadata.DefaultSupportedMedia,
				DateHandlingFlags: cliflags.DateHandlingFlags{
					Method: cliflags.DateMethodNone,
				},
				InclusionFlags: cliflags.InclusionFlags{},
				Recursive:      true,
			},
			fsys: []fs.FS{
				newInMemFS("MemFS").
					addFile("root_01.jpg").
					addFile("photos/photo_01.jpg").
					addFile("photos/photo_02.cr3").
					addFile("photos/photo_03.jpg").
					addFile("photos/summer 2023/20230801-001.jpg").
					addFile("photos/summer 2023/20230801-002.jpg").
					addFile("photos/summer 2023/20230801-003.cr3").
					addFile("@eaDir/thb1.jpg").
					addFile("photos/SYNOFILE_THUMB_0001.jpg").
					addFile("photos/summer 2023/.@__thumb/thb2.jpg").
					addFile("BLOG/blog.jpg").
					addFile("Project/Database/database_01.jpg").
					addFile("photos/database_01.jpg").
					addFile("mac/image.JPG").
					addFile("mac/._image.JPG"),
			},
			expectedFiles: map[string]fileLinks{
				"root_01.jpg":                         {image: "root_01.jpg"},
				"photos/photo_01.jpg":                 {image: "photos/photo_01.jpg"},
				"photos/photo_02.cr3":                 {image: "photos/photo_02.cr3"},
				"photos/photo_03.jpg":                 {image: "photos/photo_03.jpg"},
				"photos/summer 2023/20230801-001.jpg": {image: "photos/summer 2023/20230801-001.jpg"},
				"photos/summer 2023/20230801-002.jpg": {image: "photos/summer 2023/20230801-002.jpg"},
				"photos/summer 2023/20230801-003.cr3": {image: "photos/summer 2023/20230801-003.cr3"},
				"photos/database_01.jpg":              {image: "photos/database_01.jpg"},
				"mac/image.JPG":                       {image: "mac/image.JPG"},
			},
			expectedCounts: fileevent.NewCounts().Set(fileevent.DiscoveredImage, 9).
				Set(fileevent.DiscoveredDiscarded, 6).Value(),
		},
		{
			name: "excluded extensions",
			flags: ImportFolderOptions{
				BannedFiles:    namematcher.MustList(`@eaDir/`, `.@__thumb`, `SYNOFILE_THUMB_*.*`),
				SupportedMedia: metadata.DefaultSupportedMedia,
				DateHandlingFlags: cliflags.DateHandlingFlags{
					Method: cliflags.DateMethodNone,
				},
				InclusionFlags: cliflags.InclusionFlags{
					ExcludedExtensions: cliflags.ExtensionList{".cr3"},
				},
				Recursive: true,
			},
			fsys: []fs.FS{
				newInMemFS("MemFS").
					addFile("root_01.jpg").
					addFile("photos/photo_01.jpg").
					addFile("photos/photo_02.cr3").
					addFile("photos/photo_03.jpg").
					addFile("photos/summer 2023/20230801-001.jpg").
					addFile("photos/summer 2023/20230801-002.jpg").
					addFile("photos/summer 2023/20230801-003.cr3").
					addFile("@eaDir/thb1.jpg").
					addFile("photos/SYNOFILE_THUMB_0001.jpg").
					addFile("photos/summer 2023/.@__thumb/thb2.jpg"),
			},
			expectedFiles: map[string]fileLinks{
				"root_01.jpg":                         {image: "root_01.jpg"},
				"photos/photo_01.jpg":                 {image: "photos/photo_01.jpg"},
				"photos/photo_03.jpg":                 {image: "photos/photo_03.jpg"},
				"photos/summer 2023/20230801-001.jpg": {image: "photos/summer 2023/20230801-001.jpg"},
				"photos/summer 2023/20230801-002.jpg": {image: "photos/summer 2023/20230801-002.jpg"},
			},
			expectedCounts: fileevent.NewCounts().Set(fileevent.DiscoveredImage, 7).
				Set(fileevent.DiscoveredDiscarded, 5).Value(),
		},
		{
			name: "included extensions",
			flags: ImportFolderOptions{
				BannedFiles:    namematcher.MustList(`@eaDir/`, `.@__thumb`, `SYNOFILE_THUMB_*.*`),
				SupportedMedia: metadata.DefaultSupportedMedia,
				DateHandlingFlags: cliflags.DateHandlingFlags{
					Method: cliflags.DateMethodNone,
				},
				InclusionFlags: cliflags.InclusionFlags{
					IncludedExtensions: cliflags.ExtensionList{".cr3"},
				},
				Recursive: true,
			},
			fsys: []fs.FS{
				newInMemFS("MemFS").
					addFile("root_01.jpg").
					addFile("photos/photo_01.jpg").
					addFile("photos/photo_02.cr3").
					addFile("photos/photo_03.jpg").
					addFile("photos/summer 2023/20230801-001.jpg").
					addFile("photos/summer 2023/20230801-002.jpg").
					addFile("photos/summer 2023/20230801-003.cr3").
					addFile("@eaDir/thb1.jpg").
					addFile("photos/SYNOFILE_THUMB_0001.jpg").
					addFile("photos/summer 2023/.@__thumb/thb2.jpg"),
			},
			expectedFiles: map[string]fileLinks{
				"photos/photo_02.cr3":                 {image: "photos/photo_02.cr3"},
				"photos/summer 2023/20230801-003.cr3": {image: "photos/summer 2023/20230801-003.cr3"},
			},
			expectedCounts: fileevent.NewCounts().Set(fileevent.DiscoveredImage, 7).
				Set(fileevent.DiscoveredDiscarded, 8).Value(),
		},
		{
			name: "motion picture",
			flags: ImportFolderOptions{
				BannedFiles:    namematcher.MustList(`@eaDir/`, `.@__thumb`, `SYNOFILE_THUMB_*.*`),
				SupportedMedia: metadata.DefaultSupportedMedia,
				DateHandlingFlags: cliflags.DateHandlingFlags{
					Method: cliflags.DateMethodNone,
				},
				InclusionFlags: cliflags.InclusionFlags{},
				Recursive:      true,
			},
			fsys: []fs.FS{
				newInMemFS("MemFS").
					addFile("motion/nomotion.MP4").
					addFile("motion/PXL_20210102_221126856.MP~2").
					addFile("motion/PXL_20210102_221126856.MP~2.jpg").
					addFile("motion/PXL_20210102_221126856.MP.jpg").
					addFile("motion/PXL_20210102_221126856.MP").
					addFile("motion/20231227_152817.jpg").
					addFile("motion/20231227_152817.MP4"),
			},
			expectedFiles: map[string]fileLinks{
				"motion/PXL_20210102_221126856.MP.jpg":   {image: "motion/PXL_20210102_221126856.MP.jpg", video: "motion/PXL_20210102_221126856.MP"},
				"motion/PXL_20210102_221126856.MP~2.jpg": {image: "motion/PXL_20210102_221126856.MP~2.jpg", video: "motion/PXL_20210102_221126856.MP~2"},
				"motion/20231227_152817.jpg":             {image: "motion/20231227_152817.jpg", video: "motion/20231227_152817.MP4"},
				"motion/nomotion.MP4":                    {video: "motion/nomotion.MP4"},
			},
			expectedCounts: fileevent.NewCounts().Set(fileevent.DiscoveredImage, 3).
				Set(fileevent.DiscoveredVideo, 4).Value(),
		},

		{
			name: "date in range",
			flags: ImportFolderOptions{
				SupportedMedia: metadata.DefaultSupportedMedia,
				DateHandlingFlags: cliflags.DateHandlingFlags{
					Method: cliflags.DateMethodName,
					FilenameTimeZone: tzone.Timezone{
						TZ: time.Local,
					},
				},
				InclusionFlags: cliflags.InclusionFlags{
					DateRange: cliflags.InitDateRange("2023-08"),
				},
				Recursive: true,
			},
			fsys: []fs.FS{
				newInMemFS("MemFS").
					addFile("root_01.jpg").
					addFile("photos/photo_01.jpg").
					addFile("photos/photo_02.cr3").
					addFile("photos/photo_03.jpg").
					addFile("photos/summer 2023/20230801-001.jpg").
					addFile("photos/summer 2023/20230801-002.jpg").
					addFile("photos/summer 203/20230301-003.cr3"),
			},
			expectedFiles: map[string]fileLinks{
				"photos/summer 2023/20230801-001.jpg": {image: "photos/summer 2023/20230801-001.jpg"},
				"photos/summer 2023/20230801-002.jpg": {image: "photos/summer 2023/20230801-002.jpg"},
			},
			expectedCounts: fileevent.NewCounts().Set(fileevent.DiscoveredImage, 7).
				Set(fileevent.DiscoveredDiscarded, 5).
				Value(),
		},

		{
			name: "path as album name",
			flags: ImportFolderOptions{
				SupportedMedia:         metadata.DefaultSupportedMedia,
				UsePathAsAlbumName:     FolderModePath,
				AlbumNamePathSeparator: " ¤ ",
				InclusionFlags:         cliflags.InclusionFlags{},
				DateHandlingFlags: cliflags.DateHandlingFlags{
					Method: cliflags.DateMethodNone,
				},
				Recursive: true,
			},
			fsys: []fs.FS{
				newInMemFS("MemFS").
					addFile("root_01.jpg").
					addFile("photos/photo_01.jpg").
					addFile("photos/photo_02.cr3").
					addFile("photos/photo_03.jpg").
					addFile("photos/summer 2023/20230801-001.jpg").
					addFile("photos/summer 2023/20230801-002.jpg").
					addFile("photos/summer 2023/20230801-003.cr3"),
			},
			expectedFiles: map[string]fileLinks{
				"root_01.jpg":                         {image: "root_01.jpg"},
				"photos/photo_01.jpg":                 {image: "photos/photo_01.jpg"},
				"photos/photo_02.cr3":                 {image: "photos/photo_02.cr3"},
				"photos/photo_03.jpg":                 {image: "photos/photo_03.jpg"},
				"photos/summer 2023/20230801-001.jpg": {image: "photos/summer 2023/20230801-001.jpg"},
				"photos/summer 2023/20230801-002.jpg": {image: "photos/summer 2023/20230801-002.jpg"},
				"photos/summer 2023/20230801-003.cr3": {image: "photos/summer 2023/20230801-003.cr3"},
			},
			expectedCounts: fileevent.NewCounts().Set(fileevent.DiscoveredImage, 7).
				Value(),
			expectedAlbums: map[string][]string{
				"MemFS":                        {"root_01.jpg"},
				"MemFS ¤ photos":               {"photos/photo_01.jpg", "photos/photo_02.cr3", "photos/photo_03.jpg"},
				"MemFS ¤ photos ¤ summer 2023": {"photos/summer 2023/20230801-001.jpg", "photos/summer 2023/20230801-002.jpg", "photos/summer 2023/20230801-003.cr3"},
			},
		},
		{
			name: "folder as album name",
			flags: ImportFolderOptions{
				SupportedMedia:         metadata.DefaultSupportedMedia,
				UsePathAsAlbumName:     FolderModeFolder,
				AlbumNamePathSeparator: " ¤ ",
				InclusionFlags:         cliflags.InclusionFlags{},
				DateHandlingFlags: cliflags.DateHandlingFlags{
					Method: cliflags.DateMethodNone,
				},
				Recursive: true,
			},
			fsys: []fs.FS{
				newInMemFS("MemFS").
					addFile("root_01.jpg").
					addFile("photos/photo_01.jpg").
					addFile("photos/photo_02.cr3").
					addFile("photos/photo_03.jpg").
					addFile("photos/summer 2023/20230801-001.jpg").
					addFile("photos/summer 2023/20230801-002.jpg").
					addFile("photos/summer 2023/20230801-003.cr3"),
			},
			expectedFiles: map[string]fileLinks{
				"root_01.jpg":                         {image: "root_01.jpg"},
				"photos/photo_01.jpg":                 {image: "photos/photo_01.jpg"},
				"photos/photo_02.cr3":                 {image: "photos/photo_02.cr3"},
				"photos/photo_03.jpg":                 {image: "photos/photo_03.jpg"},
				"photos/summer 2023/20230801-001.jpg": {image: "photos/summer 2023/20230801-001.jpg"},
				"photos/summer 2023/20230801-002.jpg": {image: "photos/summer 2023/20230801-002.jpg"},
				"photos/summer 2023/20230801-003.cr3": {image: "photos/summer 2023/20230801-003.cr3"},
			},
			expectedCounts: fileevent.NewCounts().Set(fileevent.DiscoveredImage, 7).
				Value(),
			expectedAlbums: map[string][]string{
				"MemFS":       {"root_01.jpg"},
				"photos":      {"photos/photo_01.jpg", "photos/photo_02.cr3", "photos/photo_03.jpg"},
				"summer 2023": {"photos/summer 2023/20230801-001.jpg", "photos/summer 2023/20230801-002.jpg", "photos/summer 2023/20230801-003.cr3"},
			},
		},
	}

	logFile := configuration.DefaultLogFile()
	for _, c := range tc {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()

			log := application.Log{
				File:  logFile,
				Level: "INFO",
			}
			err := log.OpenLogFile()
			if err != nil {
				t.Error(err)
				return
			}
			log.Logger.Info(c.name)
			recorder := fileevent.NewRecorder(log.Logger)
			b, err := NewLocalFiles(ctx, recorder, &c.flags, c.fsys...)
			if err != nil {
				t.Error(err)
			}

			groupChan, err := b.Browse(ctx)
			if err != nil {
				t.Error(err)
			}

			results := map[string]fileLinks{}
			albums := map[string][]string{}
			for g := range groupChan {
				if err = g.Validate(); err != nil {
					t.Error(err)
					return
				}
				fileName := g.Assets[0].FileName
				links := fileLinks{}
				for _, a := range g.Assets {
					ext := path.Ext(a.FileName)
					switch b.flags.SupportedMedia.TypeFromExt(ext) {
					case metadata.TypeImage:
						links.image = a.FileName
						if g.Kind == adapters.GroupKindMotionPhoto {
							fileName = a.FileName
						}
					case metadata.TypeVideo:
						links.video = a.FileName
					}
					if a.SideCar.FileName != "" {
						links.sidecar = a.SideCar.FileName
					}
					a.Close()
				}
				results[fileName] = links

				if len(c.expectedAlbums) > 0 {
					for _, album := range g.Albums {
						albums[album.Title] = append(albums[album.Title], fileName)
					}
				}
			}

			if !reflect.DeepEqual(results, c.expectedFiles) {
				t.Errorf("file list difference\n")
				pretty.Ldiff(t, c.expectedFiles, results)
			}
			if !reflect.DeepEqual(recorder.GetCounts(), c.expectedCounts) {
				t.Errorf("counters difference\n")
				pretty.Ldiff(t, c.expectedCounts, recorder.GetCounts())
			}
			if c.expectedAlbums != nil {
				compareAlbums(t, albums, c.expectedAlbums)
			}
		})
	}
}

func compareAlbums(t *testing.T, a, b map[string][]string) {
	a = sortAlbum(a)
	b = sortAlbum(b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("album list difference\n")
		pretty.Ldiff(t, a, b)
	}
}

func sortAlbum(a map[string][]string) map[string][]string {
	for k := range a {
		sort.Strings(a[k])
	}
	return a
}
