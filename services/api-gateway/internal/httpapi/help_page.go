package httpapi

import (
	"bytes"
	"html/template"
	"net/http"
	"strings"
	"sync"
)

// helpSection groups helpRoutes for the rendered page the way
// docs/API_GATEWAY.md's own headings group them, so the page reads as a
// tour rather than a flat list of fifteen rows.
type helpSection struct {
	Title  string
	Note   string // optional, rendered under the title
	Routes []helpRoute
}

// helpSections builds the grouping fresh from helpRoutes rather than a
// second hand-maintained list, so a route can never appear on the JSON
// contract and be missing from the page, or the reverse.
func helpSections() []helpSection {
	byPath := make(map[string]helpRoute, len(helpRoutes))
	for _, r := range helpRoutes {
		byPath[r.Method+" "+r.Path] = r
	}
	pick := func(pairs ...[2]string) []helpRoute {
		out := make([]helpRoute, 0, len(pairs))
		for _, p := range pairs {
			if r, ok := byPath[p[0]+" "+p[1]]; ok {
				out = append(out, r)
			}
		}
		return out
	}
	return []helpSection{
		{
			Title: "Webhooks",
			Note:  "Where records actually come from in a real deployment.",
			Routes: pick(
				[2]string{"POST", "/v1/webhooks/payment-failed"},
				[2]string{"POST", "/v1/webhooks/payment-downtime"},
			),
		},
		{
			Title: "Batches",
			Routes: pick(
				[2]string{"POST", "/v1/batches"},
				[2]string{"GET", "/v1/batches"},
				[2]string{"GET", "/v1/batches/{batch_id}/report"},
				[2]string{"GET", "/v1/batches/{batch_id}/records"},
			),
		},
		{
			Title: "Records",
			Routes: pick(
				[2]string{"GET", "/v1/records/{record_id}/audit"},
			),
		},
		{
			Title: "Invariants",
			Note:  "Continuous proof the agent stayed inside its own rules.",
			Routes: pick(
				[2]string{"GET", "/v1/batches/{batch_id}/invariants"},
				[2]string{"GET", "/v1/invariants"},
			),
		},
		{
			Title: "Live",
			Routes: pick(
				[2]string{"WS", "/v1/batches/{batch_id}/live"},
			),
		},
		{
			Title: "Demo controls",
			Note:  "Off by default. Only present when DEMO_CONTROLS_ENABLED is set on this deployment; a production Gateway 404s on these, it does not merely reject them.",
			Routes: pick(
				[2]string{"POST", "/v1/demo/batches"},
				[2]string{"GET", "/v1/demo/scenarios"},
				[2]string{"GET", "/v1/demo/world"},
				[2]string{"POST", "/v1/demo/inject-poison"},
				[2]string{"GET", "/v1/demo/config"},
			),
		},
	}
}

// methodClass gives each HTTP method its own colour, the same family the
// dashboard already uses (emerald for a positive/creating action, blue for
// a plain read, purple for the one streaming route), so this page and the
// dashboard read as one product rather than two.
func methodClass(method string) string {
	switch method {
	case "GET":
		return "get"
	case "POST":
		return "post"
	case "WS":
		return "ws"
	default:
		return "get"
	}
}

var helpTemplate = template.Must(template.New("help").Funcs(template.FuncMap{
	"methodClass": methodClass,
}).Parse(helpPageTemplateSource))

var helpHTMLOnce = sync.OnceValue(func() []byte {
	var buf bytes.Buffer
	data := struct {
		Sections []helpSection
	}{Sections: helpSections()}
	if err := helpTemplate.Execute(&buf, data); err != nil {
		// helpSections() is built from a package-level literal and the
		// template is parsed at init time; a failure here means the
		// template itself is broken, which template.Must already would
		// have caught before this ever runs. Panicking is correct: there
		// is no partial page worth serving.
		panic("help_page: template execute: " + err.Error())
	}
	return buf.Bytes()
})

// wantsHelpHTML reports whether the request prefers the rendered page over
// the JSON contract. A browser's default Accept header lists text/html
// first; curl's default (Accept: */*) and any explicit
// Accept: application/json must keep getting the JSON that
// docs/API_GATEWAY.md documents and every other test above asserts, so
// this checks for an explicit preference rather than "would tolerate
// html".
func wantsHelpHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

const helpPageTemplateSource = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Momotaro API</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
<style>
  :root {
    --font-sans: 'Inter', system-ui, -apple-system, sans-serif;
    --font-mono: 'JetBrains Mono', 'SF Mono', monospace;
  }
  * { box-sizing: border-box; }
  body {
    margin: 0; padding: 0;
    background: #f8fafc;
    color: #0f172a;
    font-family: var(--font-sans);
    -webkit-font-smoothing: antialiased;
  }
  .wrap { max-width: 840px; margin: 0 auto; padding: 48px 24px 96px; }
  header h1 {
    font-size: 22px; font-weight: 700; margin: 0 0 4px;
    display: flex; align-items: center; gap: 10px;
  }
  header h1 .mark {
    width: 30px; height: 30px; border-radius: 8px; background: #0f172a;
    color: white; display: inline-flex; align-items: center; justify-content: center;
    font-size: 16px;
  }
  header p { color: #64748b; margin: 0 0 28px; font-size: 14px; }
  .auth-card {
    background: white; border: 1px solid #e2e8f0; border-radius: 12px;
    padding: 18px 20px; margin-bottom: 32px; font-size: 13px; line-height: 1.6; color: #334155;
  }
  .auth-card h2 { font-size: 12px; text-transform: uppercase; letter-spacing: .04em; color: #94a3b8; margin: 0 0 8px; font-weight: 600; }
  .auth-card code { font-family: var(--font-mono); background: #f1f5f9; padding: 1px 5px; border-radius: 4px; font-size: 12px; }
  section.group { margin-bottom: 28px; }
  section.group > h2 {
    font-size: 13px; text-transform: uppercase; letter-spacing: .04em; color: #94a3b8;
    font-weight: 600; margin: 0 0 4px;
  }
  section.group > .note { font-size: 12.5px; color: #94a3b8; margin: 0 0 10px; }
  details.route {
    background: white; border: 1px solid #e2e8f0; border-radius: 10px;
    margin-bottom: 6px; overflow: hidden;
  }
  details.route[open] { border-color: #cbd5e1; }
  details.route > summary {
    list-style: none; cursor: pointer; padding: 11px 14px;
    display: flex; align-items: center; gap: 10px; font-size: 13.5px;
  }
  details.route > summary::-webkit-details-marker { display: none; }
  details.route > summary::before {
    content: '›'; color: #cbd5e1; font-size: 16px; width: 10px;
    transition: transform .12s ease;
  }
  details.route[open] > summary::before { transform: rotate(90deg); }
  .badge {
    font-family: var(--font-mono); font-size: 11px; font-weight: 600;
    padding: 2px 7px; border-radius: 5px; letter-spacing: .02em; flex-shrink: 0;
  }
  .badge.get { background: #dbeafe; color: #1d4ed8; }
  .badge.post { background: #d1fae5; color: #047857; }
  .badge.ws { background: #f3e8ff; color: #7e22ce; }
  .path { font-family: var(--font-mono); font-size: 13px; color: #0f172a; }
  .desc-preview { color: #94a3b8; font-size: 12.5px; margin-left: auto; text-align: right; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 40%; }
  .route-body { padding: 0 14px 14px 14px; border-top: 1px solid #f1f5f9; }
  .route-body p { font-size: 13px; color: #334155; line-height: 1.55; margin: 10px 0; }
  .route-body .auth-note {
    font-size: 12.5px; color: #92400e; background: #fffbeb; border: 1px solid #fde68a;
    border-radius: 8px; padding: 8px 10px; margin: 10px 0 0;
  }
  footer { margin-top: 40px; font-size: 12px; color: #cbd5e1; }
</style>
</head>
<body>
<div class="wrap">
  <header>
    <h1><span class="mark">M</span> Momotaro API</h1>
    <p>Payment failure and mandate recovery agent. Every route the Gateway answers, assembled from the frozen contract at <code>docs/API_GATEWAY.md</code>. Click a route to read what it needs.</p>
  </header>

  <div class="auth-card">
    <h2>Authentication</h2>
    Every route below except this page requires an <code>X-API-Key</code> header. The two webhook routes require a second header on top of that, <code>X-Razorpay-Signature</code>: an HMAC-SHA256 hex digest of the raw request body. The one WebSocket route sends the key as the connection's subprotocol instead of a header, since a WebSocket handshake cannot set custom headers. Ask for <code>Accept: application/json</code> (or nothing) on any of these routes and you get the machine-readable contract instead of this page.
  </div>

  {{range .Sections}}
  <section class="group">
    <h2>{{.Title}}</h2>
    {{if .Note}}<p class="note">{{.Note}}</p>{{end}}
    {{range .Routes}}
    <details class="route">
      <summary>
        <span class="badge {{methodClass .Method}}">{{.Method}}</span>
        <span class="path">{{.Path}}</span>
        <span class="desc-preview">{{.Description}}</span>
      </summary>
      <div class="route-body">
        <p>{{.Description}}</p>
        <div class="auth-note">{{.Auth}}</div>
      </div>
    </details>
    {{end}}
  </section>
  {{end}}

  <footer>Generated from docs/API_GATEWAY.md. GET /v1/help with Accept: application/json for the same data as JSON.</footer>
</div>
</body>
</html>
`
