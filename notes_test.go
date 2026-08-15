package main

import (
	"strings"
	"testing"
	"time"
)

func TestRenderNotes(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"viñetas", "* uno\n* dos", "<ul><li>uno</li><li>dos</li></ul>"},
		{"guiones", "- uno\n- dos", "<ul><li>uno</li><li>dos</li></ul>"},
		{"numerada", "1. uno\n2) dos", "<ol><li>uno</li><li>dos</li></ol>"},
		{"párrafos", "hola\nadiós\n\notro", "<p>hola<br>adiós</p><p>otro</p>"},
		{"negritas", "**fuerte**", "<p><strong>fuerte</strong></p>"},
		{"cursivas", "_suave_", "<p><em>suave</em></p>"},
		{"código", "usa `go build`", "<p>usa <code>go build</code></p>"},
		{"lista tras texto", "Pasos:\n* uno", "<p>Pasos:</p><ul><li>uno</li></ul>"},
		{"cambio de lista", "* uno\n1. dos", "<ul><li>uno</li></ul><ol><li>dos</li></ol>"},
	}
	for _, c := range cases {
		if got := string(renderNotes(0, c.in)); got != c.want {
			t.Errorf("%s: renderNotes(%q)\n got %q\nwant %q", c.name, c.in, got, c.want)
		}
	}
}

func TestRenderNotesEscapa(t *testing.T) {
	got := string(renderNotes(0, `<script>alert("x")</script> y "comillas"`))
	if strings.Contains(got, "<script>") || strings.Contains(got, `alert("x")`) {
		t.Fatalf("las notas inyectaron HTML: %q", got)
	}
	// una liga con comillas no puede salirse del atributo href
	got = string(renderNotes(0, `https://ok.mx/a"onmouseover="x`))
	if strings.Contains(got, `onmouseover="x"`) || strings.Contains(got, `" onmouseover`) {
		t.Fatalf("la liga se salió del atributo: %q", got)
	}
}

func TestRenderNotesCasillas(t *testing.T) {
	got := string(renderNotes(7, "[ ] pendiente\n[x] lista\n- [X] con guion"))
	for _, want := range []string{
		`<ul class="checks">`,
		`action="/t/7/chk"`,
		`name="i" value="0"`, `name="set" value="1"`, // sin marcar: el toque la marca
		`name="i" value="1"`, `name="set" value="0"`, // marcada: el toque la desmarca
		`name="i" value="2"`,
		`<li class="chk done">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en:\n%s", want, got)
		}
	}
	// sin tarea (id 0) las casillas son decorativas, sin formularios
	if strings.Contains(string(renderNotes(0, "[ ] x")), "<form") {
		t.Error("una nota sin tarea no debería traer formularios")
	}
}

func TestToggleCheck(t *testing.T) {
	src := "Antes\n[ ] uno\n  - [x] dos\n* [ ] tres"
	if got := toggleCheck(src, 0, true); !strings.Contains(got, "[x] uno") {
		t.Errorf("marcar 0: %q", got)
	}
	// conserva sangría y guion de la línea
	if got := toggleCheck(src, 1, false); !strings.Contains(got, "  - [ ] dos") {
		t.Errorf("desmarcar 1: %q", got)
	}
	if got := toggleCheck(src, 2, true); !strings.Contains(got, "* [x] tres") {
		t.Errorf("marcar 2: %q", got)
	}
	// marcar dos veces es lo mismo que marcar una (la cola offline reintenta)
	once := toggleCheck(src, 0, true)
	if twice := toggleCheck(once, 0, true); twice != once {
		t.Errorf("no es idempotente:\n%q\n%q", once, twice)
	}
	// un índice que no existe deja la nota igual
	if got := toggleCheck(src, 9, true); got != src {
		t.Errorf("índice inexistente cambió la nota: %q", got)
	}
	// el texto que no es casilla no se toca
	if !strings.HasPrefix(toggleCheck(src, 0, true), "Antes\n") {
		t.Error("se perdió el texto previo")
	}
}

func TestDaysBetween(t *testing.T) {
	mx, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Skip("sin base de husos horarios")
	}
	// hoy a las 23:00 en México contra una fecha que Postgres entrega a
	// medianoche UTC: deben seguir siendo el mismo día
	hoy := time.Date(2026, 3, 5, 23, 0, 0, 0, mx)
	vence := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	if d := daysBetween(hoy, vence); d != 0 {
		t.Errorf("mismo día: %d", d)
	}
	if d := daysBetween(hoy, vence.AddDate(0, 0, 1)); d != 1 {
		t.Errorf("mañana: %d", d)
	}
	if d := daysBetween(hoy, vence.AddDate(0, 0, -3)); d != -3 {
		t.Errorf("hace tres días: %d", d)
	}
}

func TestSafeBack(t *testing.T) {
	ok := []string{"/", "/?l=2", "/hoy", "/buscar?q=hola%20mundo", "/t/12"}
	bad := []string{"//evil.com", "https://evil.com", "evil.com", "/x\\y", "/x\ny", strings.Repeat("/a", 200)}
	for _, v := range ok {
		if !safeBack(v) {
			t.Errorf("debería aceptar %q", v)
		}
	}
	for _, v := range bad {
		if safeBack(v) {
			t.Errorf("debería rechazar %q", v)
		}
	}
}

func TestParseDue(t *testing.T) {
	if d, ok := parseDue("2026-08-20"); !ok || d.String != "2026-08-20" {
		t.Errorf("fecha válida: %v %v", d, ok)
	}
	if d, ok := parseDue("  "); !ok || d.Valid {
		t.Errorf("vacío debe ser NULL sin error: %v %v", d, ok)
	}
	for _, v := range []string{"20/08/2026", "2026-13-01", "hoy"} {
		if _, ok := parseDue(v); ok {
			t.Errorf("debería rechazar %q", v)
		}
	}
}
