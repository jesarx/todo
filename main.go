package main

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type App struct {
	db   *sql.DB
	tmpl map[string]*template.Template
	loc  *time.Location
	hash string // hash pbkdf2 de la contraseña
	ver  string // versión de assets (hash del css/js) para cache busting
}

const schema = `
CREATE TABLE IF NOT EXISTS lists (
    id       serial PRIMARY KEY,
    name     text NOT NULL,
    position smallint NOT NULL DEFAULT 0
);
-- las tres secciones de arranque; se renombran y borran desde Ajustes
INSERT INTO lists (name, position)
SELECT * FROM (VALUES ('Personal', 0), ('Trabajo', 1), ('Casa', 2)) AS v(name, position)
WHERE NOT EXISTS (SELECT 1 FROM lists);

CREATE TABLE IF NOT EXISTS tasks (
    id         serial PRIMARY KEY,
    client_id  text UNIQUE,
    list_id    int NOT NULL REFERENCES lists(id),
    title      text NOT NULL,
    notes      text NOT NULL DEFAULT '',
    pinned     boolean NOT NULL DEFAULT false,
    done_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS tasks_list_idx ON tasks (list_id, done_at);
CREATE INDEX IF NOT EXISTS tasks_done_idx ON tasks (done_at DESC);
-- las tareas ya no llevan fecha límite: se hacen cuando se pueden
DROP INDEX IF EXISTS tasks_due_idx;
ALTER TABLE tasks DROP COLUMN IF EXISTS due_on;

CREATE TABLE IF NOT EXISTS sessions (
    token      text PRIMARY KEY,
    expires_at timestamptz NOT NULL
);
`

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash" {
		fmt.Fprintln(os.Stderr, "Escribe la contraseña y presiona Enter (se verá en pantalla):")
		r := bufio.NewReader(os.Stdin)
		pw, _ := r.ReadString('\n')
		pw = strings.TrimRight(pw, "\r\n")
		if len(pw) < 10 {
			log.Fatal("usa una contraseña de al menos 10 caracteres")
		}
		fmt.Println(hashPassword(pw))
		return
	}

	dsn := envOr("TODO_DSN", "")
	if dsn == "" {
		log.Fatal("falta TODO_DSN (ej. postgres://todo_user:pass@localhost/todo?sslmode=disable)")
	}
	hash := envOr("TODO_PASSWORD_HASH", "")
	if hash == "" {
		log.Fatal("falta TODO_PASSWORD_HASH (genera uno con: ./todo hash)")
	}
	addr := envOr("TODO_ADDR", "127.0.0.1:4200")
	loc, err := time.LoadLocation(envOr("TODO_TZ", "America/Mexico_City"))
	if err != nil {
		log.Fatal(err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(5)
	if err := db.Ping(); err != nil {
		log.Fatal("no pude conectar a Postgres: ", err)
	}
	if _, err := db.Exec(schema); err != nil {
		log.Fatal("migración: ", err)
	}

	app := &App{db: db, loc: loc, hash: hash, ver: assetVersion()}
	app.parseTemplates()

	mux := http.NewServeMux()

	// estáticos (embebidos en el binario); las URLs llevan ?v=<hash> así que
	// pueden cachearse fuerte: un deploy nuevo cambia la URL, no el caché
	static, _ := fsSub(staticFS, "static")
	mux.Handle("GET /static/", cacheControl("public, max-age=31536000, immutable",
		http.StripPrefix("/static/", http.FileServer(static))))
	mux.Handle("GET /manifest.webmanifest", serveStatic(static, "manifest.webmanifest", "application/manifest+json", "public, max-age=3600"))
	mux.Handle("GET /sw.js", serveStatic(static, "sw.js", "text/javascript", "no-cache"))
	mux.Handle("GET /favicon.svg", serveStatic(static, "favicon.svg", "image/svg+xml", "public, max-age=86400"))
	mux.Handle("GET /favicon.ico", http.NotFoundHandler())

	// sesión
	mux.HandleFunc("GET /login", app.loginPage)
	mux.HandleFunc("POST /login", app.loginPost)
	mux.HandleFunc("POST /logout", app.requireAuth(app.logout))

	// páginas
	mux.HandleFunc("GET /{$}", app.requireAuth(app.home))
	mux.HandleFunc("GET /todas", app.requireAuth(app.boardPage))
	// /hoy (la agenda por fecha) se retiró: los pendientes no llevan fecha
	mux.HandleFunc("GET /hoy", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/todas", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /buscar", app.requireAuth(app.searchPage))
	mux.HandleFunc("GET /ajustes", app.requireAuth(app.settingsPage))
	mux.HandleFunc("GET /t/{id}", app.requireAuth(app.taskPage))
	mux.HandleFunc("GET /export.csv", app.requireAuth(app.exportCSV))

	// tareas
	mux.HandleFunc("POST /t", app.requireAuth(app.taskCreate))
	mux.HandleFunc("POST /t/{id}", app.requireAuth(app.taskUpdate))
	mux.HandleFunc("POST /t/{id}/toggle", app.requireAuth(app.taskToggle))
	mux.HandleFunc("POST /t/{id}/pin", app.requireAuth(app.taskPin))
	mux.HandleFunc("POST /t/{id}/delete", app.requireAuth(app.taskDelete))
	mux.HandleFunc("POST /t/{id}/chk", app.requireAuth(app.taskCheck))
	// mismas acciones sobre una tarea que aún se identifica por su client_id:
	// la cola offline las manda antes de conocer el id que le tocó en la base
	mux.HandleFunc("POST /t/c/{cid}/toggle", app.requireAuth(app.taskToggle))
	mux.HandleFunc("POST /t/c/{cid}/delete", app.requireAuth(app.taskDelete))

	// secciones y mantenimiento
	mux.HandleFunc("POST /lista", app.requireAuth(app.listCreate))
	mux.HandleFunc("POST /lista/{id}", app.requireAuth(app.listUpdate))
	mux.HandleFunc("POST /lista/{id}/mover", app.requireAuth(app.listMove))
	mux.HandleFunc("POST /lista/{id}/delete", app.requireAuth(app.listDelete))
	mux.HandleFunc("POST /limpiar", app.requireAuth(app.purgeDone))

	srv := &http.Server{
		Addr:         addr,
		Handler:      app.secure(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	log.Println("todo escuchando en", addr)
	log.Fatal(srv.ListenAndServe())
}

// secure agrega headers de seguridad y verificación de Origin en POST.
func (a *App) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		// nada de estilos ni scripts en línea: todo vive en /static
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		if r.Method == http.MethodPost {
			// defensa CSRF: la cookie ya es SameSite=Strict; además el Origin debe coincidir
			origin := r.Header.Get("Origin")
			if origin != "" && !sameHost(origin, r.Host) {
				http.Error(w, "origen no permitido", http.StatusForbidden)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		}
		next.ServeHTTP(w, r)
	})
}

func sameHost(origin, host string) bool {
	origin = strings.TrimPrefix(origin, "https://")
	origin = strings.TrimPrefix(origin, "http://")
	return strings.EqualFold(origin, host)
}

// assetVersion deriva un hash corto del css/js embebido: cambia con cada
// deploy que los toque y sirve para versionar sus URLs (?v=...).
func assetVersion() string {
	h := sha256.New()
	for _, p := range []string{"static/style.css", "static/app.js"} {
		b, _ := staticFS.ReadFile(p)
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}

func cacheControl(value string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// today: medianoche de hoy en la zona horaria de la app.
func (a *App) today() time.Time {
	n := time.Now().In(a.loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, a.loc)
}
