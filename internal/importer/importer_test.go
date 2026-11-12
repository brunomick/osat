package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"osat/internal/store"
)

func write(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// utf16le arma un archivo como los que exporta regedit (UTF-16 con BOM, CRLF).
func utf16le(s string) []byte {
	u := utf16.Encode([]rune(strings.ReplaceAll(s, "\n", "\r\n")))
	out := []byte{0xFF, 0xFE}
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

func byName(t *testing.T, items []Item, name string) Item {
	t.Helper()
	for _, it := range items {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("no se importó %q (hay %+v)", name, items)
	return Item{}
}

func TestPuTTYRegFile(t *testing.T) {
	reg := `Windows Registry Editor Version 5.00

[HKEY_CURRENT_USER\Software\SimonTatham\PuTTY\Sessions]

[HKEY_CURRENT_USER\Software\SimonTatham\PuTTY\Sessions\AWS%20web]
"HostName"="ec2-user@10.0.0.5"
"PortNumber"=dword:00000016
"Protocol"="ssh"
"PublicKeyFile"="C:\\Users\\bruno\\keys\\aws.ppk"

[HKEY_CURRENT_USER\Software\SimonTatham\PuTTY\Sessions\db]
"HostName"="db.interno"
"PortNumber"=dword:00000d3d
"UserName"="admin"
"Protocol"="ssh"

[HKEY_CURRENT_USER\Software\SimonTatham\PuTTY\Sessions\switch]
"HostName"="192.168.0.2"
"Protocol"="telnet"

[HKEY_CURRENT_USER\Software\SimonTatham\PuTTY\Sessions\Default%20Settings]
"HostName"=""
`
	p := write(t, "putty.reg", utf16le(reg))
	existing := []store.Connection{{Protocol: store.ProtoSSH, Host: "DB.interno", Port: 3389, User: "admin"}}
	r, err := Scan(PuTTY, p, "", existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 2 {
		t.Fatalf("%d sesiones, quería 2: %+v", len(r.Items), r.Items)
	}
	web := byName(t, r.Items, "AWS web")
	if web.Host != "10.0.0.5" || web.User != "ec2-user" || web.Port != 22 || web.Group != "PuTTY" {
		t.Errorf("AWS web = %+v", web)
	}
	if web.AuthType != store.AuthKey || web.KeyPath != `C:\Users\bruno\keys\aws.ppk` {
		t.Errorf("clave: %q %q", web.AuthType, web.KeyPath)
	}
	db := byName(t, r.Items, "db")
	if db.Port != 3389 || db.AuthType != store.AuthPassword || !db.Exists {
		t.Errorf("db = %+v (debería figurar como existente)", db)
	}
	if web.Exists {
		t.Error("AWS web figura como existente")
	}
	if len(r.Skipped) != 1 || !strings.Contains(r.Skipped[0], "telnet") {
		t.Errorf("omitidas = %v", r.Skipped)
	}
}

func TestWinSCPIni(t *testing.T) {
	ini := `[Configuration\Interface]
Updates=0

[Sessions\Default%20Settings]
HostName=

[Sessions\Producci%C3%B3n/web%2001]
HostName=10.0.0.5
UserName=ubuntu
PublicKeyFile=C:%5CUsers%5Cbruno%5Ckeys%5Caws.ppk
FSProtocol=2

[Sessions\backup]
HostName=backup.lan
PortNumber=2222
UserName=root

[Sessions\ftp%20viejo]
HostName=ftp.lan
FSProtocol=5
`
	p := write(t, "WinSCP.ini", []byte(ini))
	r, err := Scan(WinSCP, p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 2 {
		t.Fatalf("%d sesiones, quería 2: %+v", len(r.Items), r.Items)
	}
	web := byName(t, r.Items, "web 01")
	if web.Group != "Producción" || web.Host != "10.0.0.5" || web.User != "ubuntu" || web.Port != 22 {
		t.Errorf("web 01 = %+v", web)
	}
	if web.AuthType != store.AuthKey || web.KeyPath != `C:\Users\bruno\keys\aws.ppk` {
		t.Errorf("clave: %q %q", web.AuthType, web.KeyPath)
	}
	bk := byName(t, r.Items, "backup")
	if bk.Group != "WinSCP" || bk.Port != 2222 || bk.AuthType != store.AuthPassword {
		t.Errorf("backup = %+v", bk)
	}
	if len(r.Skipped) != 1 || !strings.Contains(r.Skipped[0], "FTP") {
		t.Errorf("omitidas = %v", r.Skipped)
	}
}

func TestMRemoteNG(t *testing.T) {
	xml := `<?xml version="1.0" encoding="utf-8"?>
<mrng:Connections xmlns:mrng="http://mremoteng.org" Name="Connections" EncryptionEngine="AES" BlockCipherMode="GCM" KdfIterations="1000" FullFileEncryption="false" ConfVersion="2.6">
    <Node Name="AWS" Type="Container" Expanded="true">
        <Node Name="Windows" Type="Container">
            <Node Name="dc01" Type="Connection" Hostname="10.0.1.9" Protocol="RDP" Port="3389" Username="admin" Domain="CORP" />
        </Node>
        <Node Name="web" Type="Connection" Hostname="10.0.0.5" Protocol="SSH2" Port="2200" Username="ubuntu" />
    </Node>
    <Node Name="kvm" Type="Connection" Hostname="10.0.0.20" Protocol="VNC" Port="5900" />
    <Node Name="suelta" Type="Connection" Hostname="10.0.0.30" Protocol="RDP" Port="" Username="" />
</mrng:Connections>`
	p := write(t, "confCons.xml", []byte(xml))
	r, err := Scan(MRemoteNG, p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 3 {
		t.Fatalf("%d conexiones, quería 3: %+v", len(r.Items), r.Items)
	}
	dc := byName(t, r.Items, "dc01")
	if dc.Protocol != store.ProtoRDP || dc.Group != "AWS/Windows" || dc.Domain != "CORP" || dc.Port != 3389 || dc.User != "admin" {
		t.Errorf("dc01 = %+v", dc)
	}
	web := byName(t, r.Items, "web")
	if web.Protocol != store.ProtoSSH || web.Group != "AWS" || web.Port != 2200 {
		t.Errorf("web = %+v", web)
	}
	if s := byName(t, r.Items, "suelta"); s.Group != "mRemoteNG" || s.Port != 3389 {
		t.Errorf("suelta = %+v", s)
	}
	if len(r.Skipped) != 1 || !strings.Contains(r.Skipped[0], "VNC") {
		t.Errorf("omitidas = %v", r.Skipped)
	}

	enc := write(t, "enc.xml", []byte(`<?xml version="1.0" encoding="utf-8"?>
<mrng:Connections xmlns:mrng="http://mremoteng.org" FullFileEncryption="true" ConfVersion="2.6">c2VjcmV0bw==</mrng:Connections>`))
	if _, err := Scan(MRemoteNG, enc, "", nil); err == nil || !strings.Contains(err.Error(), "cifrado") {
		t.Errorf("archivo cifrado: err = %v", err)
	}
}

func TestNotFoundAndBadFiles(t *testing.T) {
	// Sin sesiones en la ubicación habitual: NotFound, no un error.
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if r, err := Scan(MRemoteNG, "", "", nil); err != nil || !r.NotFound {
		t.Errorf("mRemoteNG sin archivo: %+v, %v", r, err)
	}
	// Un archivo que no es de la herramienta da un error claro.
	other := write(t, "otro.reg", []byte("Windows Registry Editor Version 5.00\n\n[HKEY_CURRENT_USER\\Software\\Otra]\n\"x\"=\"y\"\n"))
	if _, err := Scan(PuTTY, other, "", nil); err == nil {
		t.Error("un .reg sin sesiones de PuTTY no dio error")
	}
	if _, err := Scan("filezilla", "", "", nil); err == nil {
		t.Error("origen desconocido no dio error")
	}
}

// scanMR importa un confCons.xml con los nodos dados.
func scanMR(t *testing.T, nodes string) Result {
	t.Helper()
	xml := `<?xml version="1.0" encoding="utf-8"?>
<mrng:Connections xmlns:mrng="http://mremoteng.org" Name="Connections" EncryptionEngine="AES" BlockCipherMode="GCM" KdfIterations="1000" FullFileEncryption="false" ConfVersion="2.8">` + nodes + `</mrng:Connections>`
	r, err := Scan(MRemoteNG, write(t, "confCons.xml", []byte(xml)), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func skippedFor(r Result, name string) []string {
	var out []string
	for _, s := range r.Skipped {
		if strings.Contains(s, name) {
			out = append(out, s)
		}
	}
	return out
}

func TestMRemoteNGGatewayAlways(t *testing.T) {
	r := scanMR(t, `<Node Name="ts" Type="Connection" Hostname="ts.corp" Protocol="RDP" Username="u"
		RDGatewayUsageMethod="Always" RDGatewayHostname="rds.corp" RDGatewayUseConnectionCredentials="Yes" />`)
	if it := byName(t, r.Items, "ts"); it.Gateway != "rds.corp" || len(skippedFor(r, "ts")) != 0 {
		t.Fatalf("%+v %v", it, r.Skipped)
	}
}

func TestMRemoteNGGatewayNever(t *testing.T) {
	r := scanMR(t, `<Node Name="ts" Type="Connection" Hostname="ts.corp" Protocol="RDP" Username="u"
		RDGatewayUsageMethod="Never" RDGatewayHostname="rds.corp" />`)
	if it := byName(t, r.Items, "ts"); it.Gateway != "" {
		t.Fatalf("%+v", it)
	}
}

func TestMRemoteNGGatewayDetect(t *testing.T) {
	r := scanMR(t, `<Node Name="ts" Type="Connection" Hostname="ts.corp" Protocol="RDP" Username="u"
		RDGatewayUsageMethod="Detect" RDGatewayHostname="rds.corp" RDGatewayUseConnectionCredentials="Yes" />`)
	if it := byName(t, r.Items, "ts"); it.Gateway != "rds.corp" || len(skippedFor(r, "ts")) != 1 {
		t.Fatalf("%+v %v", it, r.Skipped)
	}
}

func TestMRemoteNGGatewayInherited(t *testing.T) {
	r := scanMR(t, `<Node Name="Empresa" Type="Container" RDGatewayUsageMethod="Always" RDGatewayHostname="rds.corp" RDGatewayUseConnectionCredentials="Yes">
		<Node Name="ts" Type="Connection" Hostname="ts.corp" Protocol="RDP" Username="u" RDGatewayUsageMethod="Never" RDGatewayHostname=""
			InheritRDGatewayUsageMethod="true" InheritRDGatewayHostname="true" InheritRDGatewayUseConnectionCredentials="true" />
	</Node>`)
	if it := byName(t, r.Items, "ts"); it.Gateway != "rds.corp" {
		t.Fatalf("%+v %v", it, r.Skipped)
	}
}

func TestMRemoteNGGatewayOwnCreds(t *testing.T) {
	r := scanMR(t, `<Node Name="ts" Type="Connection" Hostname="ts.corp" Protocol="RDP" Username="u"
		RDGatewayUsageMethod="Always" RDGatewayHostname="rds.corp" RDGatewayUseConnectionCredentials="No" />`)
	if it := byName(t, r.Items, "ts"); it.Gateway != "rds.corp" || len(skippedFor(r, "ts")) != 1 {
		t.Fatalf("%+v %v", it, r.Skipped)
	}
}
