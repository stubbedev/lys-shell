package bar

import (
	"errors"
	"log"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/identity"
	"github.com/stubbedev/lys-desktop/service/clipboard"
	"github.com/stubbedev/lys-lib/xdg"
)

var errNoDataDir = errors.New("neither XDG_DATA_HOME nor HOME is an absolute path")

// clipboardStores opens where the clipboard history keeps entries
// across restarts. A store that cannot be opened is said in the log and
// left out: the history still runs, without pins or persistence.
func clipboardStores(cfg config.ClipboardConfig) clipboard.Stores {
	var stores clipboard.Stores
	if pinned, err := openClipboardStore(clipboard.OpenPinnedStore); err != nil {
		log.Printf("bar: clipboard: no pinned entries: %v", err)
	} else {
		stores.Pinned = pinned
	}
	if cfg.Persist {
		stores.History = historyStore()
	}
	return stores
}

// reconfigureClipboard applies a reloaded clipboard section. Loop
// goroutine, so persistence turns on and off in the order reloads came.
func reconfigureClipboard(clip *clipboard.Clipboard, cfg config.ClipboardConfig) {
	if err := clip.SetBudget(cfg.HistoryMiB.Budget()); err != nil {
		log.Printf("bar: clipboard history budget: %v", err)
	}
	if err := clip.SetPinnedBudget(cfg.PinnedMiB.Budget()); err != nil {
		log.Printf("bar: clipboard pinned budget: %v", err)
	}
	if cfg.Persist == clip.Persisting() {
		return
	}
	var store *clipboard.Store
	if cfg.Persist {
		if store = historyStore(); store == nil {
			return
		}
	}
	if err := clip.SetPersist(store); err != nil {
		log.Printf("bar: clipboard persistence: %v", err)
	}
}

// historyStore opens the persisted history's store, nil (said in the
// log) when it cannot be.
func historyStore() *clipboard.Store {
	store, err := openClipboardStore(clipboard.OpenHistoryStore)
	if err != nil {
		log.Printf("bar: clipboard: the history stays in memory: %v", err)
		return nil
	}
	return store
}

func openClipboardStore(open func(dataDir string) (*clipboard.Store, error)) (*clipboard.Store, error) {
	dir, ok := xdg.OS.DataDir(identity.Name)
	if !ok {
		return nil, errNoDataDir
	}
	return open(dir)
}
