package htmlout

import (
	"bytes"
	"strings"
	"testing"
)

// TestRender_NoNetworkEgress is a guardrail for the offline / air-gapped
// contract spelled out in CLAUDE.md: the generated HTML must work on a
// host with no network access. Any markup or JS API that would cause
// the browser to phone home breaks that contract.
//
// We render sampleHistory() (no URLs, no exotic characters in commit
// content) and grep the output for forbidden constructs. User-supplied
// content with HTML-special characters cannot trip this test because
// metaJSON and chunk encoding both call enc.SetEscapeHTML(true) — a
// commit message containing "<iframe" arrives in the JSON payload as
// "<iframe", which won't match the literal "<iframe" pattern.
// So a failure here means the template or the rendering pipeline
// itself introduced an egress vector — exactly the regression we want
// CI to catch.
func TestRender_NoNetworkEgress(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sampleHistory()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	forbidden := []struct {
		pat string
		why string
	}{
		{"<link ", "external stylesheet/font preload"},
		{"<script src", "external script tag"},
		{"<iframe", "external frame"},
		{"<embed ", "external embed"},
		{"<object data", "external object data"},
		{"<base href", "<base> retargets relative URLs"},
		{`<meta http-equiv="refresh"`, "meta-refresh redirect"},
		{"@import", "CSS @import"},
		{"@font-face", "CSS @font-face (may pull from URL)"},
		{"url(http", "CSS url(http://...)"},
		{`url("http`, `CSS url("http://...")`},
		{"url('http", "CSS url('http://...')"},
		{"url(//", "CSS url(// ... protocol-relative)"},
		{"fetch(", "JS fetch()"},
		{"XMLHttpRequest", "JS XMLHttpRequest"},
		{"WebSocket", "JS WebSocket"},
		{"EventSource", "JS EventSource"},
		{"sendBeacon", "navigator.sendBeacon()"},
		{"new Image(", "JS Image() ping pattern"},
	}
	for _, f := range forbidden {
		if strings.Contains(out, f.pat) {
			t.Errorf("rendered HTML contains %q (%s) — breaks zero-egress contract", f.pat, f.why)
		}
	}
}
