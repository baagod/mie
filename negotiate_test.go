package mie

import "testing"

func TestNegotiate(t *testing.T) {
    offered := []string{MIMEJSON, MIMEXML}
    cases := []struct {
        name     string
        accept   string
        ownMedia string
        offered  []string
        want     string
    }{
        {"no accept", "", MIMEJSON, offered, MIMEJSON},
        {"any", MIMEAny, MIMEJSON, offered, MIMEJSON},
        {"exact own", MIMEJSON, MIMEJSON, offered, MIMEJSON},
        {"exact offered", MIMEXML, MIMEJSON, offered, MIMEXML},
        {"params stripped", "application/json; charset=utf-8", MIMEJSON, offered, MIMEJSON},
        {"q stripped", "application/json;q=0.8", MIMEJSON, offered, MIMEJSON},
        {"family wildcard", "text/*", MIMETextPlainUTF8, offered, MIMETextPlainUTF8},
        {"app wildcard picks offered", "application/*", MIMETextPlainUTF8, offered, MIMEJSON},
        {"skip miss", "text/html, */*", MIMEJSON, offered, MIMEJSON},
        {"miss", "text/csv", MIMEJSON, offered, ""},
        {"nothing offered", "application/xml", MIMEJSON, nil, ""},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            if got := negotiate(tc.accept, tc.ownMedia, tc.offered); got != tc.want {
                t.Fatalf("negotiate(%q, %q, %v) = %q, want %q", tc.accept, tc.ownMedia, tc.offered, got, tc.want)
            }
        })
    }
}
