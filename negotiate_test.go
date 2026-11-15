package mie

import "testing"

func TestNegotiate(t *testing.T) {
    offered := []string{MIMEAppJSON, MIMEAppXML}
    cases := []struct {
        name    string
        accept  string
        ownMedia string
        offered []string
        want    string
    }{
        {"no accept", "", MIMEAppJSON, offered, MIMEAppJSON},
        {"any", MIMEAny, MIMEAppJSON, offered, MIMEAppJSON},
        {"exact own", MIMEAppJSON, MIMEAppJSON, offered, MIMEAppJSON},
        {"exact offered", MIMEAppXML, MIMEAppJSON, offered, MIMEAppXML},
        {"params stripped", "application/json; charset=utf-8", MIMEAppJSON, offered, MIMEAppJSON},
        {"q stripped", "application/json;q=0.8", MIMEAppJSON, offered, MIMEAppJSON},
        {"family wildcard", "text/*", MIMETextPlainUTF8, offered, MIMETextPlainUTF8},
        {"app wildcard picks offered", "application/*", MIMETextPlainUTF8, offered, MIMEAppJSON},
        {"skip miss", "text/html, */*", MIMEAppJSON, offered, MIMEAppJSON},
        {"miss", "text/csv", MIMEAppJSON, offered, ""},
        {"nothing offered", "application/xml", MIMEAppJSON, nil, ""},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            if got := negotiate(tc.accept, tc.ownMedia, tc.offered); got != tc.want {
                t.Fatalf("negotiate(%q, %q, %v) = %q, want %q", tc.accept, tc.ownMedia, tc.offered, got, tc.want)
            }
        })
    }
}
