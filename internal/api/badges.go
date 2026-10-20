package api

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/store"
)

// Badges are SVG images for READMEs and wikis, in the style of shields.io:
//
//	/badge/status.svg            the status page's overall status
//	/badge/{id}/status.svg       a monitor's status
//	/badge/{id}/uptime.svg       its uptime (or on-time rate) over ?period=24h, 7d, 30d or 90d
//
// ?label= replaces the left-hand text. Like the status page, they're
// public, so they exist only while the status page is on, and only for
// monitors on it.

// Shields.io's colors.
const (
	badgeGreen  = "#4c1"
	badgeLime   = "#97ca00"
	badgeYellow = "#dfb317"
	badgeOrange = "#fe7d37"
	badgeRed    = "#e05d44"
	badgeBlue   = "#007ec6"
	badgeGrey   = "#9f9f9f"
)

func (s *Server) badgeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /badge/status.svg", s.overallBadge)
	mux.HandleFunc("GET /badge/{id}/status.svg", s.statusBadge)
	mux.HandleFunc("GET /badge/{id}/uptime.svg", s.uptimeBadge)
}

func (s *Server) overallBadge(w http.ResponseWriter, r *http.Request) {
	if !s.badgesOn(w, r) {
		return
	}
	overall, err := s.pageOverall(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	color := map[string]string{"operational": badgeGreen, "degraded": badgeYellow, "outage": badgeRed, "maintenance": badgeBlue}[overall]
	writeBadge(w, r, http.StatusOK, "status", overall, color)
}

func (s *Server) statusBadge(w http.ResponseWriter, r *http.Request) {
	m, ok := s.badgeMonitor(w, r)
	if !ok {
		return
	}
	status, color := string(s.Scheduler.Status(m)), badgeGrey
	switch {
	case s.publicMaintenanceCovers(m.ID):
		status, color = "maintenance", badgeBlue
	case status == string(monitor.StatusUp):
		color = badgeGreen
	case status == string(monitor.StatusDown):
		color = badgeRed
	}
	writeBadge(w, r, http.StatusOK, m.PublicName(), status, color)
}

func (s *Server) uptimeBadge(w http.ResponseWriter, r *http.Request) {
	m, ok := s.badgeMonitor(w, r)
	if !ok {
		return
	}
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "30d"
	}
	if _, ok := periodDays[period]; !ok && period != "24h" {
		writeBadge(w, r, http.StatusBadRequest, "period", "use 24h, 7d, 30d or 90d", badgeGrey)
		return
	}
	label := "uptime " + period
	if m.Kind == monitor.KindHeartbeat {
		label = "on time " + period
	}
	ratio, err := s.ratio(r.Context(), m, period)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if ratio == nil {
		writeBadge(w, r, http.StatusOK, label, "no data", badgeGrey)
		return
	}
	writeBadge(w, r, http.StatusOK, label, formatPercent(*ratio), uptimeColor(*ratio))
}

// badgesOn writes a "not found" badge unless the status page is on.
func (s *Server) badgesOn(w http.ResponseWriter, r *http.Request) bool {
	sp, err := s.Store.StatusPage(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return false
	}
	if !sp.Enabled {
		writeBadge(w, r, http.StatusNotFound, "badge", "not found", badgeGrey)
		return false
	}
	return true
}

// badgeMonitor returns the monitor in the path if it's on the status page.
func (s *Server) badgeMonitor(w http.ResponseWriter, r *http.Request) (monitor.Monitor, bool) {
	if !s.badgesOn(w, r) {
		return monitor.Monitor{}, false
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	m, err := s.Store.GetMonitor(r.Context(), id)
	if err != nil && !store.IsNotFound(err) {
		s.internalError(w, r, err)
		return m, false
	}
	if err != nil || !m.Public {
		// The same answer for a private monitor as for none at all.
		writeBadge(w, r, http.StatusNotFound, "badge", "not found", badgeGrey)
		return m, false
	}
	return m, true
}

func uptimeColor(ratio float64) string {
	switch {
	case ratio >= 0.999:
		return badgeGreen
	case ratio >= 0.99:
		return badgeLime
	case ratio >= 0.95:
		return badgeYellow
	case ratio >= 0.9:
		return badgeOrange
	default:
		return badgeRed
	}
}

// formatPercent shows up to two decimals without trailing zeros, and never
// rounds up to 100%: 0.99999 is "99.99%".
func formatPercent(ratio float64) string {
	p := float64(int64(ratio*10000+1e-9)) / 100 // the epsilon undoes float error: 0.9993 is 9992.9999…
	return strconv.FormatFloat(p, 'f', -1, 64) + "%"
}

func writeBadge(w http.ResponseWriter, r *http.Request, code int, label, message, color string) {
	if l := r.URL.Query().Get("label"); l != "" {
		label = l
	}
	label, message = truncateRunes(label, 60), truncateRunes(message, 60)
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=60")
	// Opened directly, the image can't run anything.
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(badgeSVG(label, message, color)))
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// badgeSVG draws a flat two-part badge. Text widths are estimated for
// Verdana 11px; textLength makes the text fit its box whatever font the
// viewer has.
func badgeSVG(label, message, color string) string {
	lw, mw := textWidth(label)+10, textWidth(message)+10
	total := lw + mw
	esc := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">`, total, esc(label), esc(message))
	fmt.Fprintf(&b, `<title>%s: %s</title>`, esc(label), esc(message))
	b.WriteString(`<linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>`)
	fmt.Fprintf(&b, `<clipPath id="r"><rect width="%d" height="20" rx="3" fill="#fff"/></clipPath>`, total)
	fmt.Fprintf(&b, `<g clip-path="url(#r)"><rect width="%d" height="20" fill="#555"/><rect x="%d" width="%d" height="20" fill="%s"/><rect width="%d" height="20" fill="url(#s)"/></g>`,
		lw, lw, mw, color, total)
	b.WriteString(`<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">`)
	for _, t := range []struct {
		x, w int
		s    string
	}{{lw / 2, lw - 10, label}, {lw + mw/2, mw - 10, message}} {
		fmt.Fprintf(&b, `<text x="%d" y="15" fill="#010101" fill-opacity=".3" textLength="%d" lengthAdjust="spacingAndGlyphs">%s</text>`, t.x, t.w, esc(t.s))
		fmt.Fprintf(&b, `<text x="%d" y="14" textLength="%d" lengthAdjust="spacingAndGlyphs">%s</text>`, t.x, t.w, esc(t.s))
	}
	b.WriteString(`</g></svg>`)
	return b.String()
}

// textWidth estimates s's width in Verdana 11px.
func textWidth(s string) int {
	w := 0.0
	for _, c := range s {
		switch {
		case strings.ContainsRune("iljI.,:;!|'", c):
			w += 3.4
		case strings.ContainsRune("frt()[]{} -/", c):
			w += 4.6
		case strings.ContainsRune("mwMW%", c):
			w += 10.5
		case c >= 'A' && c <= 'Z':
			w += 7.6
		case c >= '0' && c <= '9':
			w += 7
		default:
			w += 6.6
		}
	}
	return int(w + 0.5)
}
