package pulsetest

import "github.com/stubbedev/lys-shell/service/pulse/native"

// PutCard adds or replaces a card and announces it.
func (s *Server) PutCard(c native.CardInfo) {
	put(s, s.cards, c.Index, c, native.FacilityCard)
}

// Card is the card at index as the server holds it.
func (s *Server) Card(index uint32) (native.CardInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cards[index]
	return c, ok
}

func (c *conn) cardList(tag uint32, r *native.Reader, failed func() bool) bool {
	if !r.EOF() {
		return false
	}
	if failed() {
		return true
	}
	c.s.mu.Lock()
	all := sorted(c.s.cards)
	c.s.mu.Unlock()
	var w native.Writer
	for _, card := range all {
		native.EncodeCard(&w, card, c.negotiated())
	}
	c.reply(tag, &w)
	return true
}

func (c *conn) cardInfo(tag uint32, r *native.Reader, failed func() bool) bool {
	index := r.U32()
	_, _ = r.String()
	if r.Err() != nil || !r.EOF() {
		return false
	}
	if failed() {
		return true
	}
	card, ok := c.s.Card(index)
	if !ok {
		c.fail(tag, native.ErrNoEntity)
		return true
	}
	var w native.Writer
	native.EncodeCard(&w, card, c.negotiated())
	c.reply(tag, &w)
	return true
}

func (c *conn) setCardProfile(tag uint32, r *native.Reader, failed func() bool) bool {
	index := r.U32()
	_, _ = r.String()
	profile := r.Str()
	if r.Err() != nil || !r.EOF() {
		return false
	}
	if failed() {
		return true
	}
	c.s.mu.Lock()
	card, ok := c.s.cards[index]
	known := false
	for _, p := range card.Profiles {
		known = known || p.Name == profile
	}
	if ok && known {
		card.ActiveProfile = profile
		c.s.cards[index] = card
	}
	c.s.mu.Unlock()
	if !ok || !known {
		c.fail(tag, native.ErrNoEntity)
		return true
	}
	c.reply(tag, &native.Writer{})
	c.s.Emit(native.SubscribeEvent{Facility: native.FacilityCard, Operation: native.OpChange, Index: index})
	return true
}
