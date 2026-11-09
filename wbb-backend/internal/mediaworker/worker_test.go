package mediaworker

import "testing"

func TestSessionRTSPConnectionsIncludeInputReader(t *testing.T) {
	source, output := paths(sessionRow{id: "session", generation: 1})
	snap := MediaSnapshot{RTSP: []MTXSession{{ID: "source-reader", Path: source}, {ID: "output-publisher", Path: output}, {ID: "other", Path: "wbb/other/1/source"}}}
	got := sessionRTSP(snap, source, output)
	if len(got) != 2 || got[0].ID != "source-reader" || got[1].ID != "output-publisher" {
		t.Fatalf("session RTSP connections: %+v", got)
	}
}
