package native

import "context"

// Card commands (pulsecore/native-common.h).
const (
	CmdGetCardInfo     = 88
	CmdGetCardInfoList = 89
	CmdSetCardProfile  = 90
)

// MaskCard selects card changes.
const MaskCard SubscriptionMask = 1 << FacilityCard

func init() {
	commandNames[CmdGetCardInfo] = "GET_CARD_INFO"
	commandNames[CmdGetCardInfoList] = "GET_CARD_INFO_LIST"
	commandNames[CmdSetCardProfile] = "SET_CARD_PROFILE"
}

// CardProfile is one profile a card offers: the sinks and sources it
// gives and whether it can be chosen now (protocol 29 and later; earlier
// servers report every profile available).
type CardProfile struct {
	Name        string
	Description string
	Sinks       uint32
	Sources     uint32
	Priority    uint32
	Available   bool
}

// CardPort is one port of a card and the profiles it belongs to.
type CardPort struct {
	Name        string
	Description string
	Priority    uint32
	Available   uint32
	Direction   uint8
	Profiles    []string
}

// CardInfo is one card (protocol-native.c's card_fill_tagstruct).
type CardInfo struct {
	Index         uint32
	Name          string
	OwnerModule   uint32
	Driver        string
	Profiles      []CardProfile
	ActiveProfile string
	Props         PropList
	Ports         []CardPort
}

const (
	cardProfileAvailableVersion = 29
	cardPortLatencyVersion      = 27
)

func decodeCard(version uint32) func(r *Reader) CardInfo {
	return func(r *Reader) CardInfo {
		c := CardInfo{Index: r.U32(), Name: r.Str(), OwnerModule: r.U32(), Driver: r.Str()}
		n := r.U32()
		for i := uint32(0); i < n && r.Err() == nil; i++ {
			p := CardProfile{Name: r.Str(), Description: r.Str(), Sinks: r.U32(), Sources: r.U32(), Priority: r.U32(), Available: true}
			if version >= cardProfileAvailableVersion {
				p.Available = r.U32() != 0
			}
			c.Profiles = append(c.Profiles, p)
		}
		c.ActiveProfile = r.Str()
		c.Props = r.PropList()
		ports := r.U32()
		for i := uint32(0); i < ports && r.Err() == nil; i++ {
			p := CardPort{Name: r.Str(), Description: r.Str(), Priority: r.U32(), Available: r.U32(), Direction: r.U8()}
			r.PropList()
			profiles := r.U32()
			for j := uint32(0); j < profiles && r.Err() == nil; j++ {
				p.Profiles = append(p.Profiles, r.Str())
			}
			if version >= cardPortLatencyVersion {
				r.S64()
			}
			c.Ports = append(c.Ports, p)
		}
		return c
	}
}

// EncodeCard writes a card as decodeCard reads it at version, for
// test servers.
func EncodeCard(w *Writer, c CardInfo, version uint32) {
	w.U32(c.Index)
	w.String(c.Name)
	w.U32(c.OwnerModule)
	w.String(c.Driver)
	w.Count(len(c.Profiles), "card profiles")
	for _, p := range c.Profiles {
		w.String(p.Name)
		w.String(p.Description)
		w.U32(p.Sinks)
		w.U32(p.Sources)
		w.U32(p.Priority)
		if version >= cardProfileAvailableVersion {
			available := uint32(0)
			if p.Available {
				available = 1
			}
			w.U32(available)
		}
	}
	w.OptString(c.ActiveProfile)
	w.PropList(c.Props)
	w.Count(len(c.Ports), "card ports")
	for _, p := range c.Ports {
		w.String(p.Name)
		w.String(p.Description)
		w.U32(p.Priority)
		w.U32(p.Available)
		w.U8(p.Direction)
		w.PropList(nil)
		w.Count(len(p.Profiles), "port profiles")
		for _, name := range p.Profiles {
			w.String(name)
		}
		if version >= cardPortLatencyVersion {
			w.S64(0)
		}
	}
}

// Cards lists every card.
func (c *Conn) Cards(ctx context.Context) ([]CardInfo, error) {
	return list(ctx, c, CmdGetCardInfoList, decodeCard(c.version))
}

// Card queries one card by index.
func (c *Conn) Card(ctx context.Context, index uint32) (CardInfo, error) {
	return one(ctx, c, CmdGetCardInfo, byIndexAndName(index), decodeCard(c.version))
}

// SetCardProfile makes profile the card's active one.
func (c *Conn) SetCardProfile(ctx context.Context, index uint32, profile string) error {
	w := byIndexAndName(index)
	w.String(profile)
	return c.exec(ctx, CmdSetCardProfile, w)
}
