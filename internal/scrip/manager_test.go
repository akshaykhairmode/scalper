package scrip

import "testing"

func TestManagerNewErrors(t *testing.T) {
	// A valid config needs both underlyings and segments.
	valid := DefaultManagerConfig()
	if _, err := NewManager(valid); err != nil {
		t.Fatalf("valid config: %v", err)
	}

	// A config with only underlyings (no segments) should now fail.
	if _, err := NewManager(ManagerConfig{Underlyings: testUnderlyings()}); err == nil {
		t.Fatal("underlyings-only should require segments")
	}

	cases := []ManagerConfig{
		{Underlyings: testUnderlyings()},
		{Segments: []string{"bad"}, Underlyings: testUnderlyings()},
		{Segments: []string{SegmentNSEFO}},
	}
	for i, cfg := range cases {
		if _, err := NewManager(cfg); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}

	// Duplicate scrip names must be rejected.
	dup := testUnderlyings()
	dup[1].ScripName = "NIFTY"
	if _, err := NewManager(ManagerConfig{Segments: []string{SegmentNSEFO}, Underlyings: dup}); err == nil {
		t.Fatal("duplicate scrip name should fail")
	}
}

func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		Underlyings: testUnderlyings(),
		Segments:    []string{SegmentNSEFO, SegmentBSEFO, SegmentNSECM, SegmentBSECM},
	}
}