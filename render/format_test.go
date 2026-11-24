package render

import "testing"

func TestFormat(t *testing.T) {
	v := struct{ Name string }{Name: "bob"}
	tests := []struct {
		name   string
		format Formatted
		media  string
	}{
		{"json", JSON(v), MediaJSON},
		{"xml", XML(v), MediaXML},
	}

	for _, tt := range tests {
		media, value := tt.format.Format()

		if media != tt.media {
			t.Errorf("%s: media = %q, want %q", tt.name, media, tt.media)
		}
		if value != v {
			t.Errorf("%s: value = %#v, want %#v", tt.name, value, v)
		}
	}
}
