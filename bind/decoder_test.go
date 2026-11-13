package bind

import "testing"

type stubDecoder struct{ tag string }

func (s stubDecoder) Decode([]byte, any) error { return nil }

func TestRegisterOverridesDefaultJSON(t *testing.T) {
    defer Register(MIMEAppJSON, jsonDecoder{})

    Register(MIMEAppJSON, stubDecoder{})

    if d, _ := Lookup(MIMEAppJSON); d != (stubDecoder{}) {
        t.Fatalf("decoder = %#v, want overridden stub", d)
    }
}

func TestRegisterIsCaseInsensitive(t *testing.T) {
    Register("Application/X-Stub-Case", stubDecoder{"case"})

    if d, ok := Lookup("application/x-stub-case"); !ok || d != (stubDecoder{"case"}) {
        t.Fatalf("decoder = %#v, want case-insensitive hit", d)
    }
}
