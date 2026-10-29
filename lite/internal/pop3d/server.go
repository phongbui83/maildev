// Package pop3d is a minimal POP3 server (RFC 1939 + STLS, UIDL, TOP) exposing a
// project's inbox. Login uses the project's SMTP credential; DELE removes the
// message from the project when the session ends with QUIT.
package pop3d

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/events"
	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/store"
)

type Backend struct {
	store *store.Store
	bus   *events.Bus
}

func NewBackend(st *store.Store, bus *events.Bus) *Backend { return &Backend{store: st, bus: bus} }

type Options struct {
	Hostname  string
	Addrs     []string // plaintext + STLS
	TLSAddrs  []string // implicit TLS
	TLSConfig *tls.Config
}

const idleTimeout = 10 * time.Minute

type server struct {
	be        *Backend
	opts      Options
	listeners []net.Listener
	mu        sync.Mutex
	conns     map[net.Conn]struct{}
}

// Serve starts the listeners and blocks until ctx is done.
func Serve(ctx context.Context, be *Backend, o Options) error {
	s := &server{be: be, opts: o, conns: map[net.Conn]struct{}{}}
	errCh := make(chan error, len(o.Addrs)+len(o.TLSAddrs))
	start := func(addr string, implicitTLS bool) {
		var ln net.Listener
		var err error
		if implicitTLS {
			ln, err = tls.Listen("tcp", addr, o.TLSConfig)
		} else {
			ln, err = net.Listen("tcp", addr)
		}
		if err != nil {
			errCh <- fmt.Errorf("pop3 %s: %w", addr, err)
			return
		}
		s.mu.Lock()
		s.listeners = append(s.listeners, ln)
		s.mu.Unlock()
		mode := "plain+stls"
		if implicitTLS {
			mode = "implicit-tls"
		}
		slog.Info("pop3 listening", "addr", addr, "mode", mode)
		for {
			c, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				errCh <- fmt.Errorf("pop3 accept %s: %w", addr, err)
				return
			}
			go s.handle(c, implicitTLS)
		}
	}
	for _, a := range o.Addrs {
		go start(a, false)
	}
	for _, a := range o.TLSAddrs {
		go start(a, true)
	}
	select {
	case <-ctx.Done():
		s.close()
		return nil
	case err := <-errCh:
		s.close()
		return err
	}
}

func (s *server) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ln := range s.listeners {
		_ = ln.Close()
	}
	for c := range s.conns {
		_ = c.Close()
	}
}

type msg struct {
	entry   store.IMAPEntry
	deleted bool
}

type session struct {
	srv     *server
	conn    net.Conn
	r       *bufio.Reader
	w       *bufio.Writer
	ip      string
	secure  bool
	user    string
	project *model.Project
	msgs    []*msg
}

func (s *server) handle(c net.Conn, secure bool) {
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		_ = c.Close()
	}()
	ip := ""
	if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		ip = a.IP.String()
	}
	sess := &session{srv: s, conn: c, r: bufio.NewReader(c), w: bufio.NewWriter(c), ip: ip, secure: secure}
	sess.run()
}

func (s *session) ok(format string, args ...any) error {
	fmt.Fprintf(s.w, "+OK "+format+"\r\n", args...)
	return s.w.Flush()
}

func (s *session) errf(format string, args ...any) error {
	fmt.Fprintf(s.w, "-ERR "+format+"\r\n", args...)
	return s.w.Flush()
}

func (s *session) run() {
	if err := s.ok("%s MailDev POP3 ready", s.srv.opts.Hostname); err != nil {
		return
	}
	for {
		_ = s.conn.SetDeadline(time.Now().Add(idleTimeout))
		line, err := s.r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if len(line) > 1024 {
			_ = s.errf("line too long")
			return
		}
		cmd, arg, _ := strings.Cut(line, " ")
		cmd = strings.ToUpper(cmd)
		quit, err := s.dispatch(cmd, strings.TrimSpace(arg))
		if err != nil || quit {
			return
		}
	}
}

func (s *session) dispatch(cmd, arg string) (quit bool, err error) {
	switch cmd {
	case "CAPA":
		caps := []string{"TOP", "UIDL", "USER", "PIPELINING", "IMPLEMENTATION MailDev"}
		if !s.secure && s.srv.opts.TLSConfig != nil {
			caps = append(caps, "STLS")
		}
		fmt.Fprint(s.w, "+OK Capability list follows\r\n")
		for _, c := range caps {
			fmt.Fprint(s.w, c+"\r\n")
		}
		fmt.Fprint(s.w, ".\r\n")
		return false, s.w.Flush()
	case "NOOP":
		return false, s.ok("")
	case "QUIT":
		if s.project != nil {
			s.update()
		}
		_ = s.ok("MailDev POP3 signing off")
		return true, nil
	}

	if s.project == nil {
		return false, s.authState(cmd, arg)
	}
	return false, s.transactionState(cmd, arg)
}

func (s *session) authState(cmd, arg string) error {
	switch cmd {
	case "STLS":
		if s.secure || s.srv.opts.TLSConfig == nil {
			return s.errf("TLS not available")
		}
		if err := s.ok("Begin TLS negotiation"); err != nil {
			return err
		}
		tc := tls.Server(s.conn, s.srv.opts.TLSConfig)
		if err := tc.Handshake(); err != nil {
			return err
		}
		s.conn = tc
		s.r = bufio.NewReader(tc)
		s.w = bufio.NewWriter(tc)
		s.secure = true
		s.user = ""
		return nil
	case "USER":
		if arg == "" {
			return s.errf("USER requires a name")
		}
		s.user = arg
		return s.ok("name is a valid mailbox")
	case "PASS":
		if s.user == "" {
			return s.errf("USER first")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, project, err := s.srv.be.store.Authenticate(ctx, model.CredSMTP, s.user, arg)
		if err != nil {
			slog.Warn("pop3 auth failed", "user", s.user, "client", s.ip)
			s.srv.be.store.Audit(ctx, store.AuditParams{Actor: s.user, Action: "pop3_login_failed", IP: s.ip, Protocol: "pop3", Detail: map[string]any{"tls": s.secure}})
			s.user = ""
			return s.errf("[AUTH] invalid username or password")
		}
		pid := project.ID
		s.srv.be.store.Audit(ctx, store.AuditParams{Actor: s.user, ProjectID: &pid, Action: "pop3_login", IP: s.ip, Protocol: "pop3", Detail: map[string]any{"tls": s.secure}})
		entries, err := s.srv.be.store.IMAPList(ctx, project.ID)
		if err != nil {
			return s.errf("temporary failure")
		}
		s.project = project
		for _, e := range entries {
			s.msgs = append(s.msgs, &msg{entry: e})
		}
		var size int64
		for _, m := range s.msgs {
			size += int64(m.entry.Size)
		}
		return s.ok("maildrop has %d messages (%d octets)", len(s.msgs), size)
	case "APOP", "AUTH":
		return s.errf("not supported, use USER/PASS")
	}
	return s.errf("unknown command or not authenticated")
}

func (s *session) msgByArg(arg string) (*msg, int, error) {
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 || n > len(s.msgs) {
		return nil, 0, errors.New("no such message")
	}
	m := s.msgs[n-1]
	if m.deleted {
		return nil, 0, errors.New("message is deleted")
	}
	return m, n, nil
}

func (s *session) transactionState(cmd, arg string) error {
	switch cmd {
	case "STAT":
		var n int
		var size int64
		for _, m := range s.msgs {
			if !m.deleted {
				n++
				size += int64(m.entry.Size)
			}
		}
		return s.ok("%d %d", n, size)
	case "LIST", "UIDL":
		if arg != "" {
			m, n, err := s.msgByArg(arg)
			if err != nil {
				return s.errf("%s", err)
			}
			if cmd == "LIST" {
				return s.ok("%d %d", n, m.entry.Size)
			}
			return s.ok("%d %s", n, uidl(m))
		}
		fmt.Fprint(s.w, "+OK\r\n")
		for i, m := range s.msgs {
			if m.deleted {
				continue
			}
			if cmd == "LIST" {
				fmt.Fprintf(s.w, "%d %d\r\n", i+1, m.entry.Size)
			} else {
				fmt.Fprintf(s.w, "%d %s\r\n", i+1, uidl(m))
			}
		}
		fmt.Fprint(s.w, ".\r\n")
		return s.w.Flush()
	case "RETR", "TOP":
		first, rest, _ := strings.Cut(arg, " ")
		m, _, err := s.msgByArg(first)
		if err != nil {
			return s.errf("%s", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		raw, err := s.srv.be.store.GetRawEmailByUID(ctx, s.project.ID, m.entry.UID)
		if err != nil {
			return s.errf("message unavailable")
		}
		raw = normalizeCRLF(raw)
		if cmd == "TOP" {
			lines, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil || lines < 0 {
				return s.errf("TOP msg n")
			}
			raw = topLines(raw, lines)
			fmt.Fprint(s.w, "+OK\r\n")
		} else {
			fmt.Fprintf(s.w, "+OK %d octets\r\n", len(raw))
			if !m.entry.Seen {
				m.entry.Seen = true
				_ = s.srv.be.store.IMAPSetFlags(ctx, s.project.ID, m.entry.UID, true, m.entry.Flagged, m.entry.Deleted)
				s.srv.be.bus.Publish(events.Event{Type: "updated", ProjectID: s.project.ID, EmailID: m.entry.ID, UID: m.entry.UID})
			}
		}
		if err := writeDotStuffed(s.w, raw); err != nil {
			return err
		}
		return s.w.Flush()
	case "DELE":
		m, _, err := s.msgByArg(arg)
		if err != nil {
			return s.errf("%s", err)
		}
		m.deleted = true
		return s.ok("message deleted")
	case "RSET":
		for _, m := range s.msgs {
			m.deleted = false
		}
		return s.ok("")
	case "USER", "PASS", "STLS":
		return s.errf("already authenticated")
	}
	return s.errf("unknown command")
}

// update applies DELE marks at QUIT (POP3 UPDATE state).
func (s *session) update() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, m := range s.msgs {
		if !m.deleted {
			continue
		}
		if err := s.srv.be.store.DeleteEmail(ctx, s.project.ID, m.entry.ID); err == nil {
			s.srv.be.bus.Publish(events.Event{Type: "deleted", ProjectID: s.project.ID, EmailID: m.entry.ID, UID: m.entry.UID})
		}
	}
}

func uidl(m *msg) string {
	// UIDLs must be stable and unique per maildrop: the email id is both.
	return strings.ReplaceAll(m.entry.ID.String(), "-", "")
}

var _ = uuid.Nil

func normalizeCRLF(b []byte) []byte {
	if !bytes.Contains(b, []byte("\n")) || bytes.Contains(b, []byte("\r\n")) {
		return b
	}
	return bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n"))
}

func topLines(raw []byte, n int) []byte {
	i := bytes.Index(raw, []byte("\r\n\r\n"))
	if i < 0 {
		return raw
	}
	header := raw[:i+4]
	body := raw[i+4:]
	var out bytes.Buffer
	out.Write(header)
	for n > 0 && len(body) > 0 {
		j := bytes.Index(body, []byte("\r\n"))
		if j < 0 {
			out.Write(body)
			body = nil
			break
		}
		out.Write(body[:j+2])
		body = body[j+2:]
		n--
	}
	return out.Bytes()
}

func writeDotStuffed(w io.Writer, raw []byte) error {
	for len(raw) > 0 {
		j := bytes.Index(raw, []byte("\r\n"))
		var line []byte
		if j < 0 {
			line, raw = raw, nil
		} else {
			line, raw = raw[:j], raw[j+2:]
		}
		if len(line) > 0 && line[0] == '.' {
			if _, err := w.Write([]byte(".")); err != nil {
				return err
			}
		}
		if _, err := w.Write(line); err != nil {
			return err
		}
		if _, err := w.Write([]byte("\r\n")); err != nil {
			return err
		}
	}
	_, err := w.Write([]byte(".\r\n"))
	return err
}
