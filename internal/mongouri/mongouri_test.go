package mongouri

import "testing"

func TestParse(t *testing.T) {
	u, err := Parse(" mongodb://lector:p%40ss%3Aw%2Frd@db1.example.com:27017,db2.example.com:27018/ventas?replicaSet=rs0&authSource=admin ")
	if err != nil {
		t.Fatal(err)
	}
	if u.SRV || u.User != "lector" || u.Password != "p@ss:w/rd" || len(u.Hosts) != 2 || u.Path != "/ventas" || u.Query != "replicaSet=rs0&authSource=admin" {
		t.Fatalf("Parse = %+v", u)
	}
	if h, p := u.First(); h != "db1.example.com" || p != 27017 {
		t.Fatalf("First = %s %d", h, p)
	}
	if got, want := u.Redacted(), "mongodb://lector@db1.example.com:27017,db2.example.com:27018/ventas?replicaSet=rs0&authSource=admin"; got != want {
		t.Fatalf("Redacted = %q", got)
	}
	if got, want := u.String(), "mongodb://lector:p%40ss%3Aw%2Frd@db1.example.com:27017,db2.example.com:27018/ventas?replicaSet=rs0&authSource=admin"; got != want {
		t.Fatalf("String = %q", got)
	}
	if v, ok := u.Option("REPLICASET"); !ok || v != "rs0" {
		t.Fatalf("Option = %q %v", v, ok)
	}
}

func TestParseVariants(t *testing.T) {
	for in, want := range map[string]struct {
		host string
		port int
		srv  bool
		user string
	}{
		"mongodb://localhost":                              {"localhost", 27017, false, ""},
		"mongodb://localhost:27019/":                       {"localhost", 27019, false, ""},
		"MONGODB+SRV://u@cluster0.abcd.mongodb.net/?w=1":   {"cluster0.abcd.mongodb.net", 27017, true, "u"},
		"mongodb://[::1]:27020/test":                       {"::1", 27020, false, ""},
		"mongodb://a%20b@h?authMechanism=SCRAM-SHA-256":    {"h", 27017, false, "a b"},
		"mongodb://u:@h":                                   {"h", 27017, false, "u"},
		"mongodb://us%3Aer:pw@h.example.com:1/db?tls=true": {"h.example.com", 1, false, "us:er"},
	} {
		u, err := Parse(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		h, p := u.First()
		if h != want.host || p != want.port || u.SRV != want.srv || u.User != want.user {
			t.Errorf("%s: host=%s port=%d srv=%v user=%q", in, h, p, u.SRV, u.User)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"http://h",
		"mongodb://",
		"mongodb://u:p@",
		"mongodb+srv://a.example.com,b.example.com",
		"mongodb://h x",
		"mongodb://h/db?x=1 2",
		"mongodb://%zz@h",
	} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) debería fallar", in)
		}
	}
}

func TestRoundTripKeepsWeirdPasswords(t *testing.T) {
	for _, pw := range []string{"a:b@c/d?e#f%g", "ñandú 123", "&=+"} {
		u := URI{User: "u", Password: pw, Hosts: []string{"h"}}
		back, err := Parse(u.String())
		if err != nil || back.Password != pw || back.User != "u" {
			t.Errorf("%q: %+v %v", pw, back, err)
		}
	}
}

func TestWithOption(t *testing.T) {
	u, _ := Parse("mongodb://h/db?proxyHost=otro&w=1")
	got := u.WithOption("proxyHost", "127.0.0.1").WithOption("proxyPort", "1080").Redacted()
	if want := "mongodb://h/db?w=1&proxyHost=127.0.0.1&proxyPort=1080"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	u, _ = Parse("mongodb://h")
	if got, want := u.WithOption("proxyPort", "1").Redacted(), "mongodb://h/?proxyPort=1"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
