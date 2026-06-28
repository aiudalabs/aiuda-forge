package projects_test

import "testing"

func TestLinkAndListChannels(t *testing.T) {
	st := openTemp(t)
	if err := st.LinkChannel("p1", "telegram", "chat-123", "*"); err != nil {
		t.Fatalf("LinkChannel: %v", err)
	}
	chans, err := st.Channels("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(chans) != 1 || chans[0].Target != "chat-123" || chans[0].Events != "*" {
		t.Fatalf("channels = %+v, want one telegram chat-123/*", chans)
	}
	// Upsert: re-link same target updates events, no dup.
	if err := st.LinkChannel("p1", "telegram", "chat-123", "run.failed"); err != nil {
		t.Fatal(err)
	}
	chans, _ = st.Channels("p1")
	if len(chans) != 1 || chans[0].Events != "run.failed" {
		t.Fatalf("after upsert = %+v, want events updated, no dup", chans)
	}
}

func TestChannelsForEvent(t *testing.T) {
	st := openTemp(t)
	_ = st.LinkChannel("p1", "telegram", "all", "*")
	_ = st.LinkChannel("p1", "telegram", "fails", "run.failed,billing.spend_cap_tripped")
	_ = st.LinkChannel("p1", "telegram", "done", "run.done")

	got, err := st.ChannelsForEvent("p1", "run.failed")
	if err != nil {
		t.Fatal(err)
	}
	// "all" (*) and "fails" subscribe to run.failed; "done" does not.
	targets := map[string]bool{}
	for _, c := range got {
		targets[c.Target] = true
	}
	if !targets["all"] || !targets["fails"] || targets["done"] {
		t.Fatalf("run.failed subscribers = %v, want all+fails, not done", targets)
	}
}

func TestUnlinkChannel(t *testing.T) {
	st := openTemp(t)
	_ = st.LinkChannel("p1", "telegram", "chat-1", "*")
	if err := st.UnlinkChannel("p1", "telegram", "chat-1"); err != nil {
		t.Fatal(err)
	}
	if chans, _ := st.Channels("p1"); len(chans) != 0 {
		t.Fatalf("after unlink = %+v, want empty", chans)
	}
}
