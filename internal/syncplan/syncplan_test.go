package syncplan

import (
	"reflect"
	"testing"
)

func file(rel string, size, mt int64) Entry { return Entry{Rel: rel, Size: size, ModTime: mt} }
func hashed(rel string, size, mt int64, h string) Entry {
	return Entry{Rel: rel, Size: size, ModTime: mt, Hash: h}
}
func dir(rel string) Entry  { return Entry{Rel: rel, IsDir: true, ModTime: 1} }
func link(rel string) Entry { return Entry{Rel: rel, IsLink: true} }

func TestPlan(t *testing.T) {
	const t0 = 1_700_000_000
	upSync := Options{Direction: DirUp, Mode: ModeSync, Compare: CompareTimeSize}
	upMirror := Options{Direction: DirUp, Mode: ModeMirror, Compare: CompareTimeSize}
	downSync := Options{Direction: DirDown, Mode: ModeSync, Compare: CompareTimeSize}
	downMirror := Options{Direction: DirDown, Mode: ModeMirror, Compare: CompareTimeSize}
	both := Options{Direction: DirBoth, Mode: ModeSync, Compare: CompareTimeSize}
	with := func(o Options, f func(*Options)) Options { f(&o); return o }

	cases := []struct {
		name   string
		local  []Entry
		remote []Entry
		opts   Options
		want   []Action
	}{
		// Solo en un lado.
		{"subir: falta en el servidor", []Entry{file("a.txt", 5, t0)}, nil, upSync,
			[]Action{{"a.txt", OpUpload, "no existe en el servidor", 5}}},
		{"bajar: falta en la PC", nil, []Entry{file("a.txt", 5, t0)}, downSync,
			[]Action{{"a.txt", OpDownload, "no existe en la PC", 5}}},
		{"subir: lo que está solo en el destino no se toca sin borrar",
			nil, []Entry{file("a.txt", 5, t0)}, upSync, nil},
		{"bajar: lo que está solo en el destino no se toca sin borrar",
			[]Entry{file("a.txt", 5, t0)}, nil, downMirror, nil},
		{"carpetas vacías se crean al subir", []Entry{dir("vacia"), dir("x"), dir("x/y")}, []Entry{dir("x")}, upSync,
			[]Action{{"vacia", OpMkdirRemote, "no existe en el servidor", 0}, {"x/y", OpMkdirRemote, "no existe en el servidor", 0}}},
		{"carpetas vacías se crean al bajar", nil, []Entry{dir("d")}, downSync,
			[]Action{{"d", OpMkdirLocal, "no existe en la PC", 0}}},
		{"carpeta en los dos lados no genera nada", []Entry{dir("d")}, []Entry{dir("d")}, upMirror, nil},

		// time+size en una dirección.
		{"sync: más nuevo en el origen se copia",
			[]Entry{file("a", 5, t0+10)}, []Entry{file("a", 5, t0)}, upSync,
			[]Action{{"a", OpUpload, "más nuevo en la PC", 5}}},
		{"sync: más viejo en el origen no se copia aunque cambie el tamaño",
			[]Entry{file("a", 7, t0)}, []Entry{file("a", 5, t0+10)}, upSync, nil},
		{"sync: 2 s de diferencia es la misma fecha",
			[]Entry{file("a", 5, t0+2)}, []Entry{file("a", 5, t0)}, upSync, nil},
		{"sync: 3 s ya es más nuevo",
			[]Entry{file("a", 5, t0+3)}, []Entry{file("a", 5, t0)}, upSync,
			[]Action{{"a", OpUpload, "más nuevo en la PC", 5}}},
		{"sync: misma fecha y distinto tamaño se omite con motivo",
			[]Entry{file("a", 7, t0+1)}, []Entry{file("a", 5, t0)}, upSync,
			[]Action{{"a", OpSkip, "distinto tamaño con la misma fecha", 0}}},
		{"sync bajar: más nuevo en el servidor",
			[]Entry{file("a", 5, t0)}, []Entry{file("a", 9, t0+60)}, downSync,
			[]Action{{"a", OpDownload, "más nuevo en el servidor", 9}}},
		{"sync bajar: más nuevo en la PC no se baja",
			[]Entry{file("a", 5, t0+60)}, []Entry{file("a", 9, t0)}, downSync, nil},
		{"mirror: más viejo en el origen igual se copia",
			[]Entry{file("a", 5, t0)}, []Entry{file("a", 5, t0+10)}, upMirror,
			[]Action{{"a", OpUpload, "más viejo en la PC", 5}}},
		{"mirror: misma fecha y distinto tamaño se copia",
			[]Entry{file("a", 7, t0)}, []Entry{file("a", 5, t0+2)}, upMirror,
			[]Action{{"a", OpUpload, "distinto tamaño", 7}}},
		{"mirror: iguales no genera nada",
			[]Entry{file("a", 5, t0)}, []Entry{file("a", 5, t0+1)}, upMirror, nil},
		{"mirror bajar: más viejo en el servidor",
			[]Entry{file("a", 5, t0+10)}, []Entry{file("a", 4, t0)}, downMirror,
			[]Action{{"a", OpDownload, "más viejo en el servidor", 4}}},

		// size.
		{"size: distinto tamaño se copia sin mirar fechas (sync)",
			[]Entry{file("a", 7, t0)}, []Entry{file("a", 5, t0+100)}, with(upSync, func(o *Options) { o.Compare = CompareSize }),
			[]Action{{"a", OpUpload, "distinto tamaño", 7}}},
		{"size: distinto tamaño se copia (mirror, bajar)",
			[]Entry{file("a", 7, t0+100)}, []Entry{file("a", 5, t0)}, with(downMirror, func(o *Options) { o.Compare = CompareSize }),
			[]Action{{"a", OpDownload, "distinto tamaño", 5}}},
		{"size: mismo tamaño con otra fecha no se copia",
			[]Entry{file("a", 5, t0+100)}, []Entry{file("a", 5, t0)}, with(upMirror, func(o *Options) { o.Compare = CompareSize }), nil},

		// content.
		{"content: hashes distintos se copia",
			[]Entry{hashed("a", 5, t0, "h1")}, []Entry{hashed("a", 5, t0+100, "h2")}, with(upSync, func(o *Options) { o.Compare = CompareContent }),
			[]Action{{"a", OpUpload, "distinto contenido", 5}}},
		{"content: hashes iguales no se copia aunque cambie la fecha",
			[]Entry{hashed("a", 5, t0+100, "h1")}, []Entry{hashed("a", 5, t0, "h1")}, with(upMirror, func(o *Options) { o.Compare = CompareContent }), nil},
		{"content: distinto tamaño es distinto contenido sin hash",
			[]Entry{file("a", 6, t0)}, []Entry{file("a", 5, t0)}, with(downSync, func(o *Options) { o.Compare = CompareContent }),
			[]Action{{"a", OpDownload, "distinto contenido", 5}}},
		{"content: sin hash con igual tamaño se omite",
			[]Entry{hashed("a", 5, t0, "h1")}, []Entry{file("a", 5, t0)}, with(upSync, func(o *Options) { o.Compare = CompareContent }),
			[]Action{{"a", OpSkip, "no se pudo comparar el contenido", 0}}},

		// Borrar en el destino.
		{"borrar al subir: archivos antes que su carpeta",
			[]Entry{file("keep", 1, t0)},
			[]Entry{file("keep", 1, t0), dir("old"), dir("old/sub"), file("old/sub/x", 3, t0), file("old/y", 4, t0), file("z", 2, t0)},
			with(upMirror, func(o *Options) { o.Delete = true }),
			[]Action{
				{"z", OpDeleteRemote, "no está en la PC", 2},
				{"old/y", OpDeleteRemote, "no está en la PC", 4},
				{"old/sub/x", OpDeleteRemote, "no está en la PC", 3},
				{"old/sub", OpDeleteRemote, "no está en la PC", 0},
				{"old", OpDeleteRemote, "no está en la PC", 0},
			}},
		{"borrar al bajar, en modo sync, después de las copias",
			[]Entry{file("a", 1, t0), file("b", 1, t0)}, []Entry{file("c", 1, t0), file("b", 1, t0+10)},
			with(downSync, func(o *Options) { o.Delete = true }),
			[]Action{
				{"b", OpDownload, "más nuevo en el servidor", 1},
				{"c", OpDownload, "no existe en la PC", 1},
				{"a", OpDeleteLocal, "no está en el servidor", 1},
			}},

		// Ambas direcciones.
		{"both: copia lo que falta en cada lado",
			[]Entry{file("solo-pc", 1, t0), dir("dpc")}, []Entry{file("solo-srv", 2, t0), dir("dsrv")}, both,
			[]Action{
				{"dpc", OpMkdirRemote, "no existe en el servidor", 0},
				{"dsrv", OpMkdirLocal, "no existe en la PC", 0},
				{"solo-pc", OpUpload, "no existe en el servidor", 1},
				{"solo-srv", OpDownload, "no existe en la PC", 2},
			}},
		{"both: gana el más nuevo",
			[]Entry{file("a", 1, t0+10), file("b", 1, t0)}, []Entry{file("a", 2, t0), file("b", 2, t0+10)}, both,
			[]Action{{"a", OpUpload, "más nuevo en la PC", 1}, {"b", OpDownload, "más nuevo en el servidor", 2}}},
		{"both: misma fecha y distinto tamaño se omite",
			[]Entry{file("a", 1, t0)}, []Entry{file("a", 2, t0+2)}, both,
			[]Action{{"a", OpSkip, "distinto tamaño con la misma fecha", 0}}},
		{"both: iguales no genera nada",
			[]Entry{file("a", 1, t0)}, []Entry{file("a", 1, t0+1)}, both, nil},
		{"both: nunca borra aunque se pida",
			[]Entry{file("a", 1, t0)}, []Entry{file("a", 1, t0)}, with(both, func(o *Options) { o.Delete = true; o.Mode = ModeMirror }), nil},
		{"both size: la fecha decide hacia dónde",
			[]Entry{file("a", 1, t0)}, []Entry{file("a", 2, t0+10)}, with(both, func(o *Options) { o.Compare = CompareSize }),
			[]Action{{"a", OpDownload, "más nuevo en el servidor", 2}}},
		{"both size: mismo tamaño no se copia aunque la fecha difiera",
			[]Entry{file("a", 1, t0)}, []Entry{file("a", 1, t0+10)}, with(both, func(o *Options) { o.Compare = CompareSize }), nil},
		{"both content: distinto contenido gana el más nuevo",
			[]Entry{hashed("a", 3, t0+10, "x")}, []Entry{hashed("a", 3, t0, "y")}, with(both, func(o *Options) { o.Compare = CompareContent }),
			[]Action{{"a", OpUpload, "más nuevo en la PC", 3}}},
		{"both content: distinto contenido con la misma fecha se omite",
			[]Entry{hashed("a", 3, t0, "x")}, []Entry{hashed("a", 3, t0, "y")}, with(both, func(o *Options) { o.Compare = CompareContent }),
			[]Action{{"a", OpSkip, "distinto contenido con la misma fecha", 0}}},

		// Omitidos.
		{"enlace simbólico en el origen",
			[]Entry{link("l")}, nil, upSync,
			[]Action{{"l", OpSkip, "enlace simbólico (no se sigue)", 0}}},
		{"enlace simbólico en el destino aunque se borre",
			nil, []Entry{link("l")}, with(upMirror, func(o *Options) { o.Delete = true }),
			[]Action{{"l", OpSkip, "enlace simbólico (no se sigue)", 0}}},
		{"enlace simbólico en ambas direcciones",
			[]Entry{file("l", 1, t0)}, []Entry{link("l")}, both,
			[]Action{{"l", OpSkip, "enlace simbólico (no se sigue)", 0}}},
		{"carpeta de un lado y archivo del otro",
			[]Entry{dir("x")}, []Entry{file("x", 1, t0)}, with(upMirror, func(o *Options) { o.Delete = true }),
			[]Action{{"x", OpSkip, "en un lado es carpeta y en el otro archivo", 0}}},

		// Exclusiones.
		{"excluir por nombre, por carpeta y por ruta",
			[]Entry{
				dir(".git"), file(".git/config", 1, t0),
				dir("src"), dir("src/node_modules"), file("src/node_modules/m.js", 1, t0),
				file("src/app.log", 1, t0), file("src/main.go", 1, t0),
				dir("build"), dir("build/out"), file("build/out/bin", 1, t0), file("build/keep", 1, t0),
			},
			[]Entry{file("srv.log", 1, t0), dir("node_modules")},
			with(upMirror, func(o *Options) {
				o.Delete = true
				o.Exclude = []string{" .git", "node_modules ", "*.log", "build/out", "", "["}
			}),
			[]Action{
				{"build", OpMkdirRemote, "no existe en el servidor", 0},
				{"build/keep", OpUpload, "no existe en el servidor", 1},
				{"src", OpMkdirRemote, "no existe en el servidor", 0},
				{"src/main.go", OpUpload, "no existe en el servidor", 1},
			}},
		{"sin listados no hay acciones", nil, nil, both, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Plan(c.local, c.remote, c.opts)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Plan =\n%+v\nquería\n%+v", got, c.want)
			}
		})
	}
}

func TestExcluded(t *testing.T) {
	cases := []struct {
		rel      string
		patterns []string
		want     bool
	}{
		{"a/.git/HEAD", []string{".git"}, true},
		{".git", []string{".git"}, true},
		{"a/b.log", []string{"*.log"}, true},
		{"a/b.LOG", []string{"*.log"}, false},
		{"build/out/x", []string{"build/out"}, true},
		{"src/build/out/x", []string{"build/out"}, false},
		{"src/build/out/x", []string{"*/build/out"}, true},
		{"a/b/c.txt", []string{"a/*/c.txt"}, true},
		{"gitx", []string{".git"}, false},
		{"a", []string{"", "  "}, false},
		{"a", []string{"["}, false},
		{"a", nil, false},
	}
	for _, c := range cases {
		if got := Excluded(c.rel, c.patterns); got != c.want {
			t.Errorf("Excluded(%q, %q) = %v, quería %v", c.rel, c.patterns, got, c.want)
		}
	}
}
