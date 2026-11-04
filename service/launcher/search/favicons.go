package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/stubbedev/lys-lib/atomicfile"
)

const (
	// faviconRefsMax bounds one listing of icons.
	faviconRefsMax = 50000
	// faviconBatch bounds the ids of one query for icon data.
	faviconBatch = 200
	// faviconWidth is the size wanted: a row's icon on a high-density
	// screen.
	faviconWidth = 32
	// faviconMaxSide refuses an image no favicon needs to be: this data
	// came from the web.
	faviconMaxSide = 512
	pngMagic       = `x'89504E47'`
)

// browserFavicons is how a browser's icon database is read: listings of
// (page or icon URL, icon id, width), and the data of an id.
type browserFavicons struct {
	database string
	refs     []string
	data     string
}

var faviconsOf = map[browserKind]browserFavicons{
	browserFirefox: {
		database: "favicons.sqlite",
		refs: []string{
			`SELECT p.page_url, i.id, i.width FROM moz_pages_w_icons p
				JOIN moz_icons_to_pages m ON m.page_id = p.id JOIN moz_icons i ON i.id = m.icon_id
				WHERE substr(i.data, 1, 4) = ` + pngMagic + ` ORDER BY p.id, i.id LIMIT ?`,
			`SELECT icon_url, id, width FROM moz_icons WHERE root = 1 AND substr(data, 1, 4) = ` + pngMagic + ` ORDER BY id LIMIT ?`,
		},
		data: `SELECT id, data FROM moz_icons WHERE id IN `,
	},
	browserChromium: {
		database: "Favicons",
		refs: []string{
			`SELECT m.page_url, b.id, b.width FROM icon_mapping m JOIN favicon_bitmaps b ON b.icon_id = m.icon_id
				WHERE substr(b.image_data, 1, 4) = ` + pngMagic + ` ORDER BY m.id, b.id LIMIT ?`,
		},
		data: `SELECT id, image_data FROM favicon_bitmaps WHERE id IN `,
	},
}

// pageHost is the host a page's icon is filed under: one icon for a site,
// whichever of its pages was listed.
func pageHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// betterFavicon reports whether width suits a row more than current: the
// smallest at least faviconWidth wide, else the largest there is.
func betterFavicon(width, current int64) bool {
	switch {
	case width >= faviconWidth && current >= faviconWidth:
		return width < current
	case width >= faviconWidth:
		return true
	case current >= faviconWidth:
		return false
	}
	return width > current
}

// faviconRef is the best icon found for a host so far.
type faviconRef struct {
	id, width int64
}

// bestFavicons picks an icon per wanted host; among equals the first
// listed stays, and the listings are ordered, so the pick is stable.
func bestFavicons(ctx context.Context, db *sql.DB, queries []string, wanted map[string]bool) (map[string]faviconRef, error) {
	best := map[string]faviconRef{}
	for _, query := range queries {
		if err := scanFaviconRefs(ctx, db, query, wanted, best); err != nil {
			return nil, err
		}
	}
	return best, nil
}

func scanFaviconRefs(ctx context.Context, db *sql.DB, query string, wanted map[string]bool, best map[string]faviconRef) (err error) {
	rows, err := db.QueryContext(ctx, query, faviconRefsMax)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var raw string
		var ref faviconRef
		if err := rows.Scan(&raw, &ref.id, &ref.width); err != nil {
			return err
		}
		host := pageHost(raw)
		if !wanted[host] {
			continue
		}
		if cur, ok := best[host]; !ok || betterFavicon(ref.width, cur.width) {
			best[host] = ref
		}
	}
	return rows.Err()
}

// faviconFile is where host's icon is kept: named by a hash, so the cache
// directory's listing does not read as a browsing history.
func faviconFile(iconDir, host string) string {
	sum := sha256.Sum256([]byte(host))
	return filepath.Join(iconDir, hex.EncodeToString(sum[:16])+".png")
}

// validFavicon checks data is an image of a sensible size before anything
// is allowed to decode all of it.
func validFavicon(data []byte) error {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if cfg.Width > faviconMaxSide || cfg.Height > faviconMaxSide {
		return fmt.Errorf("%dx%d is larger than a favicon", cfg.Width, cfg.Height)
	}
	return nil
}

// saveFavicons writes the data of the icons refs names under iconDir, in
// batches, and returns each id's host.
func saveFavicons(ctx context.Context, db *sql.DB, dataQuery, iconDir string, refs map[string]faviconRef) (map[string]string, error) {
	hostsOf := make(map[int64][]string, len(refs))
	ids := make([]any, 0, len(refs))
	saved := make(map[string]string, len(refs))
	for host, ref := range refs {
		path := faviconFile(iconDir, host)
		if _, err := os.Stat(path); err == nil {
			saved[host] = path
			continue
		}
		if _, seen := hostsOf[ref.id]; !seen {
			ids = append(ids, ref.id)
		}
		hostsOf[ref.id] = append(hostsOf[ref.id], host)
	}
	if len(ids) == 0 {
		return saved, nil
	}
	if err := os.MkdirAll(iconDir, 0o700); err != nil {
		return saved, err
	}
	var errs []error
	for start := 0; start < len(ids); start += faviconBatch {
		batch := ids[start:min(start+faviconBatch, len(ids))]
		err := saveFaviconBatch(ctx, db, dataQuery, batch, func(id int64, data []byte) {
			if err := validFavicon(data); err != nil {
				errs = append(errs, fmt.Errorf("icon %d: %w", id, err))
				return
			}
			for _, host := range hostsOf[id] {
				path := faviconFile(iconDir, host)
				if err := atomicfile.WriteFile(path, data, 0o600); err != nil {
					errs = append(errs, err)
					continue
				}
				saved[host] = path
			}
		})
		if err != nil {
			return saved, errors.Join(append(errs, err)...)
		}
	}
	return saved, errors.Join(errs...)
}

func saveFaviconBatch(ctx context.Context, db *sql.DB, dataQuery string, ids []any, save func(int64, []byte)) (err error) {
	placeholders := "(?" + strings.Repeat(",?", len(ids)-1) + ")"
	rows, err := db.QueryContext(ctx, dataQuery+placeholders, ids...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var id int64
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return err
		}
		save(id, data)
	}
	return rows.Err()
}

// readFavicons brings iconDir up to date with the icons of the pages'
// hosts and returns the host of each icon it holds. An icon once saved is
// kept: sites rarely change theirs, and this runs whenever a browser
// writes to its icon database.
func readFavicons(ctx context.Context, kind browserKind, dir, iconDir string, pages browserPages) (map[string]string, error) {
	wanted := map[string]bool{}
	for _, list := range [][]browserPage{pages.bookmarks, pages.visited} {
		for _, p := range list {
			if isWebURL(p.url) {
				wanted[pageHost(p.url)] = true
			}
		}
	}
	src := faviconsOf[kind]
	var saved map[string]string
	err := withDatabaseCopy(ctx, filepath.Join(dir, src.database), func(db *sql.DB) error {
		refs, err := bestFavicons(ctx, db, src.refs, wanted)
		if err != nil {
			return err
		}
		saved, err = saveFavicons(ctx, db, src.data, iconDir, refs)
		return err
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return saved, err
}
