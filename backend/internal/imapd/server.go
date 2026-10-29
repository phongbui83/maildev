// Package imapd exposes each project's inbox as a single IMAP mailbox ("INBOX")
// so that desktop clients such as Outlook or Thunderbird can read captured mail.
// Login uses the project's SMTP credential.
package imapd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	gomessage "github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"

	"github.com/phongbui83/maildev/internal/events"
	"github.com/phongbui83/maildev/internal/ingest"
	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/store"
)

const mailboxName = "INBOX"

type Backend struct {
	store  *store.Store
	bus    *events.Bus
	ingest *ingest.Ingestor
}

func NewBackend(st *store.Store, bus *events.Bus, in *ingest.Ingestor) *Backend {
	return &Backend{store: st, bus: bus, ingest: in}
}

type msg struct {
	entry store.IMAPEntry
}

func (m *msg) flags() []imap.Flag {
	var f []imap.Flag
	if m.entry.Seen {
		f = append(f, imap.FlagSeen)
	}
	if m.entry.Flagged {
		f = append(f, imap.FlagFlagged)
	}
	if m.entry.Deleted {
		f = append(f, imap.FlagDeleted)
	}
	return f
}

type session struct {
	be   *Backend
	conn *imapserver.Conn
	ip   string

	cred    *model.Credential
	project *model.Project

	mu       sync.Mutex
	selected bool
	msgs     []*msg // sequence order (index+1 = seqnum)
	rawCache map[int64][]byte
}

func (b *Backend) newSession(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
	ip := ""
	if addr, ok := c.NetConn().RemoteAddr().(*net.TCPAddr); ok {
		ip = addr.IP.String()
	}
	return &session{be: b, conn: c, ip: ip, rawCache: map[int64][]byte{}}, nil, nil
}

func ctxTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

func noSuchMailbox() error {
	return &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeNonExistent, Text: "Only INBOX exists on this server"}
}

func readOnly(text string) error {
	return &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeCannot, Text: text}
}

func (s *session) Close() error { return nil }

func (s *session) Login(username, password string) error {
	ctx, cancel := ctxTimeout()
	defer cancel()
	_, tlsOn := s.conn.NetConn().(*tls.Conn)
	cred, project, err := s.be.store.Authenticate(ctx, model.CredSMTP, username, password)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			slog.Warn("imap auth failed", "user", username, "client", s.ip)
			s.be.store.Audit(ctx, store.AuditParams{Actor: username, Action: "imap_login_failed", IP: s.ip, Protocol: "imap", Detail: map[string]any{"tls": tlsOn}})
			return imapserver.ErrAuthFailed
		}
		slog.Error("imap auth error", "err", err)
		return &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeUnavailable, Text: "Temporary failure"}
	}
	s.cred, s.project = cred, project
	pid := project.ID
	s.be.store.Audit(ctx, store.AuditParams{Actor: username, ProjectID: &pid, Action: "imap_login", IP: s.ip, Protocol: "imap", Detail: map[string]any{"tls": tlsOn}})
	return nil
}

func (s *session) requireAuth() error {
	if s.project == nil {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Not authenticated"}
	}
	return nil
}

func (s *session) load() error {
	ctx, cancel := ctxTimeout()
	defer cancel()
	entries, err := s.be.store.IMAPList(ctx, s.project.ID)
	if err != nil {
		return err
	}
	s.msgs = s.msgs[:0]
	for _, e := range entries {
		s.msgs = append(s.msgs, &msg{entry: e})
	}
	return nil
}

func (s *session) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	if err := s.requireAuth(); err != nil {
		return nil, err
	}
	if !strings.EqualFold(mailbox, mailboxName) {
		return nil, noSuchMailbox()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, err
	}
	s.selected = true
	ctx, cancel := ctxTimeout()
	defer cancel()
	uidValidity, nextUID, err := s.be.store.ProjectUIDState(ctx, s.project.ID)
	if err != nil {
		return nil, err
	}
	flags := []imap.Flag{imap.FlagSeen, imap.FlagFlagged, imap.FlagDeleted}
	var firstUnseen uint32
	for i, m := range s.msgs {
		if !m.entry.Seen {
			firstUnseen = uint32(i + 1)
			break
		}
	}
	return &imap.SelectData{
		Flags:             flags,
		PermanentFlags:    append(append([]imap.Flag{}, flags...), imap.FlagWildcard),
		NumMessages:       uint32(len(s.msgs)),
		FirstUnseenSeqNum: firstUnseen,
		UIDNext:           imap.UID(nextUID),
		UIDValidity:       uidValidity,
	}, nil
}

func (s *session) Unselect() error {
	s.mu.Lock()
	s.selected = false
	s.msgs = nil
	s.rawCache = map[int64][]byte{}
	s.mu.Unlock()
	return nil
}

func (s *session) Create(mailbox string, options *imap.CreateOptions) error {
	return readOnly("This server exposes a single read-only INBOX per project")
}
func (s *session) Delete(mailbox string) error { return readOnly("Mailboxes cannot be deleted") }
func (s *session) Rename(mailbox, newName string, options *imap.RenameOptions) error {
	return readOnly("Mailboxes cannot be renamed")
}
func (s *session) Subscribe(mailbox string) error   { return nil }
func (s *session) Unsubscribe(mailbox string) error { return nil }

func (s *session) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	if err := s.requireAuth(); err != nil {
		return err
	}
	if len(patterns) == 0 {
		return w.WriteList(&imap.ListData{Attrs: []imap.MailboxAttr{imap.MailboxAttrNoSelect}, Delim: '/'})
	}
	for _, p := range patterns {
		if imapserver.MatchList(mailboxName, '/', ref, p) {
			data := &imap.ListData{Mailbox: mailboxName, Delim: '/', Attrs: []imap.MailboxAttr{imap.MailboxAttrNoInferiors}}
			if options.ReturnSubscribed {
				data.Attrs = append(data.Attrs, imap.MailboxAttrSubscribed)
			}
			if options.ReturnStatus != nil {
				st, err := s.Status(mailboxName, options.ReturnStatus)
				if err != nil {
					return err
				}
				data.Status = st
			}
			return w.WriteList(data)
		}
	}
	return nil
}

func (s *session) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	if err := s.requireAuth(); err != nil {
		return nil, err
	}
	if !strings.EqualFold(mailbox, mailboxName) {
		return nil, noSuchMailbox()
	}
	ctx, cancel := ctxTimeout()
	defer cancel()
	entries, err := s.be.store.IMAPList(ctx, s.project.ID)
	if err != nil {
		return nil, err
	}
	uidValidity, nextUID, err := s.be.store.ProjectUIDState(ctx, s.project.ID)
	if err != nil {
		return nil, err
	}
	data := &imap.StatusData{Mailbox: mailboxName}
	if options.NumMessages {
		n := uint32(len(entries))
		data.NumMessages = &n
	}
	if options.UIDNext {
		data.UIDNext = imap.UID(nextUID)
	}
	if options.UIDValidity {
		data.UIDValidity = uidValidity
	}
	if options.NumUnseen {
		var n uint32
		for _, e := range entries {
			if !e.Seen {
				n++
			}
		}
		data.NumUnseen = &n
	}
	if options.NumDeleted {
		var n uint32
		for _, e := range entries {
			if e.Deleted {
				n++
			}
		}
		data.NumDeleted = &n
	}
	if options.Size {
		var sz int64
		for _, e := range entries {
			sz += int64(e.Size)
		}
		data.Size = &sz
	}
	if options.NumRecent {
		n := uint32(0)
		data.NumRecent = &n
	}
	return data, nil
}

// Append accepts messages into INBOX (clients may copy drafts or sent items here);
// they are captured like any other message and never delivered.
func (s *session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	if err := s.requireAuth(); err != nil {
		return nil, err
	}
	if !strings.EqualFold(mailbox, mailboxName) {
		_, _ = io.Copy(io.Discard, r)
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeTryCreate, Text: "Only INBOX exists on this server"}
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := ctxTimeout()
	defer cancel()
	email, err := s.be.ingest.Ingest(ctx, ingest.Envelope{Project: s.project, Credential: s.cred, Source: "imap", ClientIP: s.ip}, raw)
	if err != nil {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Could not store message"}
	}
	seen := false
	for _, f := range options.Flags {
		if strings.EqualFold(string(f), string(imap.FlagSeen)) {
			seen = true
		}
	}
	if seen {
		_ = s.be.store.IMAPSetFlags(ctx, s.project.ID, email.UID, true, false, false)
	}
	uidValidity, _, _ := s.be.store.ProjectUIDState(ctx, s.project.ID)
	return &imap.AppendData{UIDValidity: uidValidity, UID: imap.UID(email.UID)}, nil
}

// refresh reconciles the session view with the database, emitting EXPUNGE and
// EXISTS updates. Must be called with s.mu held.
func (s *session) refresh(w *imapserver.UpdateWriter, allowExpunge bool) error {
	if !s.selected {
		return nil
	}
	ctx, cancel := ctxTimeout()
	defer cancel()
	entries, err := s.be.store.IMAPList(ctx, s.project.ID)
	if err != nil {
		return err
	}
	current := make(map[int64]store.IMAPEntry, len(entries))
	for _, e := range entries {
		current[e.UID] = e
	}
	changed := false
	// Removed messages: iterate in reverse so sequence numbers stay valid.
	if allowExpunge {
		for i := len(s.msgs) - 1; i >= 0; i-- {
			if _, ok := current[s.msgs[i].entry.UID]; !ok {
				s.msgs = append(s.msgs[:i], s.msgs[i+1:]...)
				if w != nil {
					if err := w.WriteExpunge(uint32(i + 1)); err != nil {
						return err
					}
				}
				changed = true
			}
		}
	}
	// Flag changes made elsewhere (web UI).
	for i, m := range s.msgs {
		if e, ok := current[m.entry.UID]; ok && (e.Seen != m.entry.Seen || e.Flagged != m.entry.Flagged || e.Deleted != m.entry.Deleted) {
			m.entry = e
			if w != nil {
				if err := w.WriteMessageFlags(uint32(i+1), imap.UID(e.UID), m.flags()); err != nil {
					return err
				}
			}
		}
	}
	// New messages.
	known := make(map[int64]bool, len(s.msgs))
	for _, m := range s.msgs {
		known[m.entry.UID] = true
	}
	for _, e := range entries { // entries are uid-ordered
		if !known[e.UID] {
			s.msgs = append(s.msgs, &msg{entry: e})
			changed = true
		}
	}
	if changed && w != nil {
		return w.WriteNumMessages(uint32(len(s.msgs)))
	}
	return nil
}

func (s *session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refresh(w, allowExpunge)
}

func (s *session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	if s.project == nil {
		<-stop
		return nil
	}
	ch, cancel := s.be.bus.Subscribe(s.project.ID)
	defer cancel()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-ch:
		case <-ticker.C:
		}
		s.mu.Lock()
		err := s.refresh(w, true)
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
}

func (s *session) raw(uid int64) ([]byte, error) {
	if b, ok := s.rawCache[uid]; ok {
		return b, nil
	}
	ctx, cancel := ctxTimeout()
	defer cancel()
	b, err := s.be.store.GetRawEmailByUID(ctx, s.project.ID, uid)
	if err != nil {
		return nil, err
	}
	if len(s.rawCache) > 64 {
		s.rawCache = map[int64][]byte{}
	}
	s.rawCache[uid] = b
	return b, nil
}

// forEach calls f for every message in numSet, in sequence order. Caller holds s.mu.
func (s *session) forEach(numSet imap.NumSet, f func(seq uint32, m *msg) error) error {
	numSet = s.staticNumSet(numSet)
	for i, m := range s.msgs {
		seq := uint32(i + 1)
		var hit bool
		switch set := numSet.(type) {
		case imap.SeqSet:
			ss := set
			hit = ss.Contains(seq)
		case imap.UIDSet:
			hit = set.Contains(imap.UID(m.entry.UID))
		}
		if hit {
			if err := f(seq, m); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *session) staticNumSet(numSet imap.NumSet) imap.NumSet {
	if imap.IsSearchRes(numSet) {
		return imap.UIDSet{}
	}
	switch set := numSet.(type) {
	case imap.SeqSet:
		max := uint32(len(s.msgs))
		out := make(imap.SeqSet, len(set))
		copy(out, set)
		for i := range out {
			staticRange(&out[i].Start, &out[i].Stop, max)
		}
		return out
	case imap.UIDSet:
		var max uint32
		if n := len(s.msgs); n > 0 {
			max = uint32(s.msgs[n-1].entry.UID)
		}
		out := make(imap.UIDSet, len(set))
		copy(out, set)
		for i := range out {
			staticRange((*uint32)(&out[i].Start), (*uint32)(&out[i].Stop), max)
		}
		return out
	}
	return numSet
}

func staticRange(start, stop *uint32, max uint32) {
	dyn := false
	if *start == 0 {
		*start, dyn = max, true
	}
	if *stop == 0 {
		*stop, dyn = max, true
	}
	if dyn && *start > *stop {
		*start, *stop = *stop, *start
	}
}

func (s *session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var toDelete []int64
	for _, m := range s.msgs {
		if !m.entry.Deleted {
			continue
		}
		if uids != nil && !uids.Contains(imap.UID(m.entry.UID)) {
			continue
		}
		toDelete = append(toDelete, m.entry.UID)
	}
	if len(toDelete) == 0 {
		return nil
	}
	ctx, cancel := ctxTimeout()
	defer cancel()
	if err := s.be.store.IMAPExpunge(ctx, s.project.ID, toDelete); err != nil {
		return err
	}
	del := make(map[int64]bool, len(toDelete))
	for _, u := range toDelete {
		del[u] = true
	}
	for i := len(s.msgs) - 1; i >= 0; i-- {
		if del[s.msgs[i].entry.UID] {
			s.msgs = append(s.msgs[:i], s.msgs[i+1:]...)
			if err := w.WriteExpunge(uint32(i + 1)); err != nil {
				return err
			}
		}
	}
	s.be.ingest.Notify("deleted", s.project.ID, [16]byte{}, 0)
	return nil
}

func (s *session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	markSeen := false
	for _, bs := range options.BodySection {
		if !bs.Peek {
			markSeen = true
		}
	}
	for _, bs := range options.BinarySection {
		if !bs.Peek {
			markSeen = true
		}
	}
	return s.forEach(numSet, func(seq uint32, m *msg) error {
		if markSeen && !m.entry.Seen {
			m.entry.Seen = true
			ctx, cancel := ctxTimeout()
			_ = s.be.store.IMAPSetFlags(ctx, s.project.ID, m.entry.UID, true, m.entry.Flagged, m.entry.Deleted)
			cancel()
			s.be.ingest.Notify("updated", s.project.ID, m.entry.ID, m.entry.UID)
		}
		rw := w.CreateMessage(seq)
		rw.WriteUID(imap.UID(m.entry.UID))
		if options.Flags {
			rw.WriteFlags(m.flags())
		}
		if options.InternalDate {
			rw.WriteInternalDate(m.entry.ReceivedAt)
		}
		if options.RFC822Size {
			rw.WriteRFC822Size(int64(m.entry.Size))
		}
		needRaw := options.Envelope || options.BodyStructure != nil || len(options.BodySection) > 0 || len(options.BinarySection) > 0 || len(options.BinarySectionSize) > 0
		if needRaw {
			raw, err := s.raw(m.entry.UID)
			if err != nil {
				_ = rw.Close()
				return err
			}
			if options.Envelope {
				rw.WriteEnvelope(envelope(raw))
			}
			if options.BodyStructure != nil {
				rw.WriteBodyStructure(imapserver.ExtractBodyStructure(bytes.NewReader(raw)))
			}
			for _, bs := range options.BodySection {
				buf := imapserver.ExtractBodySection(bytes.NewReader(raw), bs)
				wc := rw.WriteBodySection(bs, int64(len(buf)))
				_, werr := wc.Write(buf)
				cerr := wc.Close()
				if werr != nil {
					return werr
				}
				if cerr != nil {
					return cerr
				}
			}
			for _, bs := range options.BinarySection {
				buf := imapserver.ExtractBinarySection(bytes.NewReader(raw), bs)
				wc := rw.WriteBinarySection(bs, int64(len(buf)))
				_, werr := wc.Write(buf)
				cerr := wc.Close()
				if werr != nil {
					return werr
				}
				if cerr != nil {
					return cerr
				}
			}
			for _, bss := range options.BinarySectionSize {
				rw.WriteBinarySectionSize(bss, imapserver.ExtractBinarySectionSize(bytes.NewReader(raw), bss))
			}
		}
		return rw.Close()
	})
}

func envelope(raw []byte) *imap.Envelope {
	br := bufio.NewReader(bytes.NewReader(raw))
	header, err := textproto.ReadHeader(br)
	if err != nil {
		return &imap.Envelope{}
	}
	return imapserver.ExtractEnvelope(header)
}

func (s *session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	s.mu.Lock()
	err := s.forEach(numSet, func(seq uint32, m *msg) error {
		seen, flagged, deleted := m.entry.Seen, m.entry.Flagged, m.entry.Deleted
		apply := func(set bool) {
			for _, f := range flags.Flags {
				switch strings.ToLower(string(f)) {
				case strings.ToLower(string(imap.FlagSeen)):
					seen = set
				case strings.ToLower(string(imap.FlagFlagged)):
					flagged = set
				case strings.ToLower(string(imap.FlagDeleted)):
					deleted = set
				}
			}
		}
		switch flags.Op {
		case imap.StoreFlagsSet:
			seen, flagged, deleted = false, false, false
			apply(true)
		case imap.StoreFlagsAdd:
			apply(true)
		case imap.StoreFlagsDel:
			apply(false)
		}
		if seen == m.entry.Seen && flagged == m.entry.Flagged && deleted == m.entry.Deleted {
			return nil
		}
		m.entry.Seen, m.entry.Flagged, m.entry.Deleted = seen, flagged, deleted
		ctx, cancel := ctxTimeout()
		defer cancel()
		if err := s.be.store.IMAPSetFlags(ctx, s.project.ID, m.entry.UID, seen, flagged, deleted); err != nil {
			return err
		}
		s.be.ingest.Notify("updated", s.project.ID, m.entry.ID, m.entry.UID)
		return nil
	})
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if !flags.Silent {
		return s.Fetch(w, numSet, &imap.FetchOptions{Flags: true})
	}
	return nil
}

func (s *session) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	return nil, readOnly("COPY is not supported: only INBOX exists")
}

func (s *session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var data imap.SearchData
	var seqSet imap.SeqSet
	var uidSet imap.UIDSet
	for i, m := range s.msgs {
		seq := uint32(i + 1)
		ok, err := s.match(seq, m, criteria)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		uidSet.AddNum(imap.UID(m.entry.UID))
		var num uint32
		switch kind {
		case imapserver.NumKindSeq:
			seqSet.AddNum(seq)
			num = seq
		case imapserver.NumKindUID:
			num = uint32(m.entry.UID)
		}
		if data.Min == 0 || num < data.Min {
			data.Min = num
		}
		if num > data.Max {
			data.Max = num
		}
		data.Count++
	}
	switch kind {
	case imapserver.NumKindSeq:
		data.All = seqSet
	case imapserver.NumKindUID:
		data.All = uidSet
	}
	return &data, nil
}

func (s *session) match(seq uint32, m *msg, c *imap.SearchCriteria) (bool, error) {
	for _, set := range c.SeqNum {
		ss := s.staticNumSet(set).(imap.SeqSet)
		if !ss.Contains(seq) {
			return false, nil
		}
	}
	for _, set := range c.UID {
		if !s.staticNumSet(set).(imap.UIDSet).Contains(imap.UID(m.entry.UID)) {
			return false, nil
		}
	}
	if !matchDate(m.entry.ReceivedAt, c.Since, c.Before) {
		return false, nil
	}
	has := func(f imap.Flag) bool {
		switch strings.ToLower(string(f)) {
		case strings.ToLower(string(imap.FlagSeen)):
			return m.entry.Seen
		case strings.ToLower(string(imap.FlagFlagged)):
			return m.entry.Flagged
		case strings.ToLower(string(imap.FlagDeleted)):
			return m.entry.Deleted
		}
		return false
	}
	for _, f := range c.Flag {
		if !has(f) {
			return false, nil
		}
	}
	for _, f := range c.NotFlag {
		if has(f) {
			return false, nil
		}
	}
	if c.Larger != 0 && int64(m.entry.Size) <= c.Larger {
		return false, nil
	}
	if c.Smaller != 0 && int64(m.entry.Size) >= c.Smaller {
		return false, nil
	}
	needRaw := len(c.Header) > 0 || len(c.Body) > 0 || len(c.Text) > 0 || !c.SentSince.IsZero() || !c.SentBefore.IsZero()
	if needRaw {
		raw, err := s.raw(m.entry.UID)
		if err != nil {
			return false, err
		}
		ent, _ := gomessage.Read(bytes.NewReader(raw))
		if ent == nil {
			return false, nil
		}
		header := mail.Header{Header: ent.Header}
		for _, hc := range c.Header {
			if !matchHeaderFields(header.FieldsByKey(hc.Key), hc.Value) {
				return false, nil
			}
		}
		if !c.SentSince.IsZero() || !c.SentBefore.IsZero() {
			t, err := header.Date()
			if err != nil || !matchDate(t, c.SentSince, c.SentBefore) {
				return false, nil
			}
		}
		for _, text := range c.Text {
			ent, _ := gomessage.Read(bytes.NewReader(raw))
			if ent == nil || !matchEntity(ent, text, true) {
				return false, nil
			}
		}
		for _, body := range c.Body {
			ent, _ := gomessage.Read(bytes.NewReader(raw))
			if ent == nil || !matchEntity(ent, body, false) {
				return false, nil
			}
		}
	}
	for i := range c.Not {
		ok, err := s.match(seq, m, &c.Not[i])
		if err != nil {
			return false, err
		}
		if ok {
			return false, nil
		}
	}
	for i := range c.Or {
		a, err := s.match(seq, m, &c.Or[i][0])
		if err != nil {
			return false, err
		}
		b, err := s.match(seq, m, &c.Or[i][1])
		if err != nil {
			return false, err
		}
		if !a && !b {
			return false, nil
		}
	}
	return true, nil
}

func matchDate(t, since, before time.Time) bool {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	if !since.IsZero() && t.Before(since) {
		return false
	}
	if !before.IsZero() && !t.Before(before) {
		return false
	}
	return true
}

func matchHeaderFields(fields gomessage.HeaderFields, pattern string) bool {
	if pattern == "" {
		return fields.Len() > 0
	}
	pattern = strings.ToLower(pattern)
	for fields.Next() {
		v, _ := fields.Text()
		if strings.Contains(strings.ToLower(v), pattern) {
			return true
		}
	}
	return false
}

func matchEntity(e *gomessage.Entity, pattern string, includeHeader bool) bool {
	if pattern == "" {
		return true
	}
	if includeHeader && matchHeaderFields(e.Header.Fields(), pattern) {
		return true
	}
	if mr := e.MultipartReader(); mr != nil {
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			} else if err != nil {
				return false
			}
			if matchEntity(part, pattern, includeHeader) {
				return true
			}
		}
		return false
	}
	t, _, err := e.Header.ContentType()
	if err != nil {
		return false
	}
	if !strings.HasPrefix(t, "text/") && !strings.HasPrefix(t, "message/") {
		return false
	}
	buf, err := io.ReadAll(e.Body)
	if err != nil {
		return false
	}
	return bytes.Contains(bytes.ToLower(buf), bytes.ToLower([]byte(pattern)))
}

// Options controls listeners.
type Options struct {
	Addrs     []string // plaintext + STARTTLS
	TLSAddrs  []string // implicit TLS
	TLSConfig *tls.Config
}

type logger struct{}

func (logger) Printf(format string, args ...any) { slog.Info(fmt.Sprintf("imap: "+format, args...)) }

// Serve starts all IMAP listeners and blocks until ctx is done.
func Serve(ctx context.Context, be *Backend, o Options) error {
	caps := imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}, imap.CapUIDPlus: {}, imap.CapLiteralPlus: {}, imap.CapUnselect: {}}
	srv := imapserver.New(&imapserver.Options{
		NewSession:   be.newSession,
		Caps:         caps,
		Logger:       logger{},
		TLSConfig:    o.TLSConfig,
		InsecureAuth: true,
	})
	errCh := make(chan error, len(o.Addrs)+len(o.TLSAddrs))
	for _, addr := range o.Addrs {
		go func(addr string) {
			slog.Info("imap listening", "addr", addr, "mode", "plain+starttls")
			if err := srv.ListenAndServe(addr); err != nil && !errors.Is(err, net.ErrClosed) {
				errCh <- fmt.Errorf("imap %s: %w", addr, err)
			}
		}(addr)
	}
	for _, addr := range o.TLSAddrs {
		go func(addr string) {
			slog.Info("imap listening", "addr", addr, "mode", "implicit-tls")
			if err := srv.ListenAndServeTLS(addr); err != nil && !errors.Is(err, net.ErrClosed) {
				errCh <- fmt.Errorf("imaps %s: %w", addr, err)
			}
		}(addr)
	}
	select {
	case <-ctx.Done():
		return srv.Close()
	case err := <-errCh:
		_ = srv.Close()
		return err
	}
}
