package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *App) parseTemplates() {
	funcs := template.FuncMap{
		"v":     func() string { return a.ver },
		"date":  a.fmtDateY,
		"when":  a.fmtWhen,
		"notes": func(t Task) template.HTML { return renderNotes(t.ID, t.Notes) },
		// dict arma un contexto para el parcial de tarea, que necesita la
		// tarea y el "volver a" de la pantalla donde se está pintando
		"dict": func(kv ...any) map[string]any {
			m := make(map[string]any, len(kv)/2)
			for i := 0; i+1 < len(kv); i += 2 {
				if k, ok := kv[i].(string); ok {
					m[k] = kv[i+1]
				}
			}
			return m
		},
	}
	pages := []string{"login.html", "home.html", "task.html", "todas.html", "buscar.html", "ajustes.html"}
	a.tmpl = make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		a.tmpl[p] = template.Must(template.New(p).Funcs(funcs).
			ParseFS(templateFS, "templates/layout.html", "templates/parts.html", "templates/"+p))
	}
}

func (a *App) render(w http.ResponseWriter, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tmpl[page].ExecuteTemplate(w, "layout.html", data); err != nil {
		log.Println("render:", err)
	}
}

// page arma los datos comunes del cascarón (encabezado y título) para no
// repetirlos en cada handler.
func (a *App) page(nav, title string, data map[string]any) map[string]any {
	if data == nil {
		data = map[string]any{}
	}
	data["Nav"] = nav
	data["Title"] = title
	return data
}

func (a *App) fail(w http.ResponseWriter, err error) {
	log.Println("error:", err)
	http.Error(w, "algo salió mal: "+err.Error(), http.StatusInternalServerError)
}

// ---- lista principal ----

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	lists, err := a.listLists()
	if err != nil || len(lists) == 0 {
		a.fail(w, fmt.Errorf("secciones: %v", err))
		return
	}
	cur := lists[0]
	if v, _ := strconv.Atoi(r.URL.Query().Get("l")); v > 0 {
		for _, l := range lists {
			if l.ID == v {
				cur = l
				break
			}
		}
	}
	open, err := a.listTasks(cur.ID, false)
	if err != nil {
		a.fail(w, err)
		return
	}
	done, _ := a.listTasks(cur.ID, true)
	a.render(w, "home.html", a.page("home", cur.Name, map[string]any{
		"Lists": lists, "Cur": cur, "Open": open, "Done": done,
		"DoneTotal": a.countDone(cur.ID), "Back": fmt.Sprintf("/?l=%d", cur.ID),
	}))
}

// ---- todas: los pendientes de todas las secciones de un vistazo ----

func (a *App) boardPage(w http.ResponseWriter, r *http.Request) {
	cols, total, err := a.board()
	if err != nil {
		a.fail(w, err)
		return
	}
	a.render(w, "todas.html", a.page("todas", "Todas", map[string]any{
		"Cols": cols, "Total": total, "Back": "/todas", "Wide": true,
	}))
}

// ---- búsqueda ----

func (a *App) searchPage(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	data := map[string]any{"Q": q, "Back": "/buscar?q=" + url.QueryEscape(q)}
	if utf8.RuneCountInString(q) >= 2 {
		res, err := a.searchTasks(q)
		if err != nil {
			a.fail(w, err)
			return
		}
		data["Results"] = res
		data["Searched"] = true
	}
	a.render(w, "buscar.html", a.page("buscar", "Buscar", data))
}

// ---- detalle de una tarea ----

func (a *App) taskPage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	t, err := a.getTask(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lists, err := a.listLists()
	if err != nil {
		a.fail(w, err)
		return
	}
	a.render(w, "task.html", a.page("home", t.Title, map[string]any{
		"T": t, "Lists": lists, "Back": fmt.Sprintf("/t/%d", t.ID),
	}))
}

// ---- acciones sobre tareas ----

func (a *App) taskCreate(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" || utf8.RuneCountInString(title) > maxTitle {
		http.Error(w, "título inválido", http.StatusBadRequest)
		return
	}
	notes := strings.TrimSpace(r.FormValue("notes"))
	if utf8.RuneCountInString(notes) > maxNotes {
		http.Error(w, "notas demasiado largas", http.StatusBadRequest)
		return
	}
	listID, err := a.resolveList(r.FormValue("list_id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	var clientID sql.NullString
	if v := strings.TrimSpace(r.FormValue("client_id")); v != "" && len(v) <= 64 {
		clientID = sql.NullString{String: v, Valid: true}
	}
	// idempotencia de la cola offline: reintentar el mismo registro no duplica
	if _, err := a.db.Exec(`INSERT INTO tasks (client_id, list_id, title, notes, pinned)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (client_id) DO NOTHING`,
		clientID, listID, title, notes, r.FormValue("pinned") != ""); err != nil {
		a.fail(w, err)
		return
	}
	a.redirect(w, r, fmt.Sprintf("/?l=%d", listID))
}

func (a *App) taskUpdate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" || utf8.RuneCountInString(title) > maxTitle {
		http.Error(w, "título inválido", http.StatusBadRequest)
		return
	}
	notes := strings.TrimSpace(r.FormValue("notes"))
	if utf8.RuneCountInString(notes) > maxNotes {
		http.Error(w, "notas demasiado largas", http.StatusBadRequest)
		return
	}
	listID, err := a.resolveList(r.FormValue("list_id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	if _, err := a.db.Exec(`UPDATE tasks SET title = $1, notes = $2,
		list_id = $3, pinned = $4, updated_at = now() WHERE id = $5`,
		title, notes, listID, r.FormValue("pinned") != "", id); err != nil {
		a.fail(w, err)
		return
	}
	a.redirect(w, r, fmt.Sprintf("/?l=%d", listID))
}

func (a *App) taskToggle(w http.ResponseWriter, r *http.Request) {
	id, ok := a.taskID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// el cliente manda el estado deseado ("set"), no un "invierte": así un
	// reintento de la cola offline nunca deshace lo que ya se aplicó
	switch r.FormValue("set") {
	case "done":
		a.db.Exec(`UPDATE tasks SET done_at = now(), updated_at = now() WHERE id = $1 AND done_at IS NULL`, id)
	case "open":
		a.db.Exec(`UPDATE tasks SET done_at = NULL, updated_at = now() WHERE id = $1`, id)
	default:
		a.db.Exec(`UPDATE tasks SET done_at = CASE WHEN done_at IS NULL THEN now() END,
			updated_at = now() WHERE id = $1`, id)
	}
	a.redirect(w, r, a.taskURL(id))
}

func (a *App) taskPin(w http.ResponseWriter, r *http.Request) {
	id, ok := a.taskID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	a.db.Exec(`UPDATE tasks SET pinned = $1, updated_at = now() WHERE id = $2`, r.FormValue("set") == "1", id)
	a.redirect(w, r, a.taskURL(id))
}

func (a *App) taskDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := a.taskID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	dest := a.taskURL(id)
	a.db.Exec(`DELETE FROM tasks WHERE id = $1`, id)
	a.redirect(w, r, dest)
}

// taskCheck marca o desmarca una casilla dentro de las notas ("[ ] algo"),
// sin abrir la tarea.
func (a *App) taskCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := a.taskID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	idx, err := strconv.Atoi(r.FormValue("i"))
	if err != nil || idx < 0 || idx > 500 {
		http.Error(w, "índice inválido", http.StatusBadRequest)
		return
	}
	var notes string
	if err := a.db.QueryRow(`SELECT notes FROM tasks WHERE id = $1`, id).Scan(&notes); err != nil {
		http.NotFound(w, r)
		return
	}
	updated := toggleCheck(notes, idx, r.FormValue("set") == "1")
	if updated != notes {
		a.db.Exec(`UPDATE tasks SET notes = $1, updated_at = now() WHERE id = $2`, updated, id)
	}
	a.redirect(w, r, a.taskURL(id))
}

// taskID acepta las dos formas de nombrar una tarea: su id de la base
// (/t/12/…) o el client_id que le puso el navegador (/t/c/<uuid>/…), que es
// lo único que conoce la cola offline mientras el alta no se ha sincronizado.
func (a *App) taskID(r *http.Request) (int, bool) {
	if s := r.PathValue("id"); s != "" {
		id, err := strconv.Atoi(s)
		return id, err == nil && id > 0
	}
	cid := r.PathValue("cid")
	if cid == "" || len(cid) > 64 {
		return 0, false
	}
	var id int
	err := a.db.QueryRow(`SELECT id FROM tasks WHERE client_id = $1`, cid).Scan(&id)
	return id, err == nil
}

// taskURL: la sección donde vive la tarea, para volver a donde estabas.
func (a *App) taskURL(id int) string {
	var list int
	if err := a.db.QueryRow(`SELECT list_id FROM tasks WHERE id = $1`, id).Scan(&list); err != nil {
		return "/"
	}
	return fmt.Sprintf("/?l=%d", list)
}

// resolveList valida la sección recibida; si ya no existe (p. ej. se borró
// mientras la tarea esperaba en la cola offline) cae en la primera.
func (a *App) resolveList(v string) (int, error) {
	id, _ := strconv.Atoi(v)
	var ok bool
	a.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM lists WHERE id = $1)`, id).Scan(&ok)
	if ok {
		return id, nil
	}
	return a.firstListID()
}

// ---- secciones (pestañas de arriba, se administran en Ajustes) ----

func validListName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= maxListName
}

func (a *App) listCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if !validListName(name) {
		http.Error(w, fmt.Sprintf("nombre inválido (máximo %d caracteres)", maxListName), http.StatusBadRequest)
		return
	}
	var n int
	a.db.QueryRow(`SELECT count(*) FROM lists`).Scan(&n)
	if n >= maxLists {
		http.Error(w, fmt.Sprintf("máximo %d secciones", maxLists), http.StatusBadRequest)
		return
	}
	a.db.Exec(`INSERT INTO lists (name, position) VALUES ($1, $2)`, name, n)
	http.Redirect(w, r, "/ajustes", http.StatusSeeOther)
}

func (a *App) listUpdate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	name := strings.TrimSpace(r.FormValue("name"))
	if !validListName(name) {
		http.Error(w, fmt.Sprintf("nombre inválido (máximo %d caracteres)", maxListName), http.StatusBadRequest)
		return
	}
	a.db.Exec(`UPDATE lists SET name = $1 WHERE id = $2`, name, id)
	http.Redirect(w, r, "/ajustes", http.StatusSeeOther)
}

// listMove intercambia la posición con la sección vecina (las flechas de
// Ajustes): es el orden en que salen las pestañas.
func (a *App) listMove(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	lists, err := a.listLists()
	if err != nil {
		a.fail(w, err)
		return
	}
	at := -1
	for i, l := range lists {
		if l.ID == id {
			at = i
		}
	}
	other := at - 1
	if r.FormValue("dir") == "down" {
		other = at + 1
	}
	if at < 0 || other < 0 || other >= len(lists) {
		http.Redirect(w, r, "/ajustes", http.StatusSeeOther)
		return
	}
	// las posiciones guardadas pueden venir repetidas o con huecos; se
	// reescriben todas según el orden ya intercambiado
	lists[at], lists[other] = lists[other], lists[at]
	tx, err := a.db.Begin()
	if err != nil {
		a.fail(w, err)
		return
	}
	defer tx.Rollback()
	for i, l := range lists {
		if _, err := tx.Exec(`UPDATE lists SET position = $1 WHERE id = $2`, i, l.ID); err != nil {
			a.fail(w, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	http.Redirect(w, r, "/ajustes", http.StatusSeeOther)
}

func (a *App) listDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	var n int
	a.db.QueryRow(`SELECT count(*) FROM lists`).Scan(&n)
	if n <= 1 {
		http.Error(w, "debe existir al menos una sección", http.StatusBadRequest)
		return
	}
	// sus tareas se mudan a la primera sección restante en vez de perderse
	if _, err := a.db.Exec(`UPDATE tasks SET list_id =
		(SELECT id FROM lists WHERE id <> $1 ORDER BY position, id LIMIT 1)
		WHERE list_id = $1`, id); err != nil {
		a.fail(w, err)
		return
	}
	a.db.Exec(`DELETE FROM lists WHERE id = $1`, id)
	http.Redirect(w, r, "/ajustes", http.StatusSeeOther)
}

// ---- ajustes ----

func (a *App) settingsPage(w http.ResponseWriter, r *http.Request) {
	lists, err := a.listLists()
	if err != nil {
		a.fail(w, err)
		return
	}
	var open, done int
	a.db.QueryRow(`SELECT count(*) FILTER (WHERE done_at IS NULL),
		count(*) FILTER (WHERE done_at IS NOT NULL) FROM tasks`).Scan(&open, &done)
	a.render(w, "ajustes.html", a.page("ajustes", "Ajustes", map[string]any{
		"Lists": lists, "Open": open, "Done": done, "Max": maxLists, "MaxName": maxListName,
	}))
}

// purgeDone borra las tareas hechas más viejas que N días (0 = todas). Es la
// única forma de borrar en bloque; una tarea suelta se borra desde su
// detalle.
func (a *App) purgeDone(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.Atoi(r.FormValue("days"))
	if err != nil || days < 0 {
		http.Error(w, "parámetro inválido", http.StatusBadRequest)
		return
	}
	if days == 0 {
		a.db.Exec(`DELETE FROM tasks WHERE done_at IS NOT NULL`)
	} else {
		a.db.Exec(`DELETE FROM tasks WHERE done_at < now() - make_interval(days => $1)`, days)
	}
	http.Redirect(w, r, "/ajustes", http.StatusSeeOther)
}

// exportCSV: respaldo legible de todo, por si algún día quieres irte a otra
// herramienta (o solo revisar el histórico en una hoja de cálculo).
func (a *App) exportCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT l.name, t.title, t.notes, t.pinned, t.created_at, t.done_at
		FROM tasks t JOIN lists l ON l.id = t.list_id ORDER BY t.created_at`)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="todo.csv"`)
	c := csv.NewWriter(w)
	defer c.Flush()
	c.Write([]string{"seccion", "tarea", "notas", "fijada", "agregada", "terminada"})
	for rows.Next() {
		var section, title, notes string
		var done sql.NullTime
		var pinned bool
		var created time.Time
		if err := rows.Scan(&section, &title, &notes, &pinned, &created, &done); err != nil {
			return
		}
		doneStr := ""
		if done.Valid {
			doneStr = done.Time.In(a.loc).Format("2006-01-02 15:04")
		}
		c.Write([]string{section, title, notes,
			map[bool]string{true: "sí", false: ""}[pinned],
			created.In(a.loc).Format("2006-01-02 15:04"), doneStr})
	}
}

// ---- utilidades de navegación ----

// redirect vuelve a donde estaba el usuario: las pantallas mandan un campo
// "back" (la agenda, la búsqueda o el detalle) y si no viene se usa la
// sección de la tarea.
func (a *App) redirect(w http.ResponseWriter, r *http.Request, def string) {
	dest := def
	if b := r.FormValue("back"); safeBack(b) {
		dest = b
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// safeBack solo acepta rutas internas: nada de "//host" ni URLs absolutas.
func safeBack(v string) bool {
	return strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "//") &&
		!strings.Contains(v, "\\") && !strings.Contains(v, "\n") && len(v) <= 200
}
