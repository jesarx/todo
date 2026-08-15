package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// List es una de las secciones de arriba (las pestañas). Se administran
// desde Ajustes: crear, renombrar, mover y borrar.
type List struct {
	ID   int
	Name string
	Pos  int
	Open int // pendientes abiertos, para el contador de la pestaña
}

type Task struct {
	ID        int
	ListID    int
	ListName  string
	Title     string
	Notes     string
	DueOn     sql.NullTime
	Pinned    bool
	DoneAt    sql.NullTime
	CreatedAt time.Time
	// calculados al leer, para que las plantillas no hagan cuentas de fechas
	DueLabel string
	DueState string // "over" | "today" | "soon" | ""
}

const taskCols = `t.id, t.list_id, l.name, t.title, t.notes, t.due_on, t.pinned, t.done_at, t.created_at`

const maxTitle = 300
const maxNotes = 8000
const maxLists = 6
const maxListName = 14

// ---- secciones ----

func (a *App) listLists() ([]List, error) {
	rows, err := a.db.Query(`
		SELECT l.id, l.name, l.position,
		       (SELECT count(*) FROM tasks t WHERE t.list_id = l.id AND t.done_at IS NULL)
		FROM lists l ORDER BY l.position, l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []List
	for rows.Next() {
		var l List
		if err := rows.Scan(&l.ID, &l.Name, &l.Pos, &l.Open); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// firstListID: a dónde caen las tareas cuya sección ya no existe (por ejemplo
// una que esperaba en la cola offline mientras se borraba su sección).
func (a *App) firstListID() (int, error) {
	var id int
	err := a.db.QueryRow(`SELECT id FROM lists ORDER BY position, id LIMIT 1`).Scan(&id)
	return id, err
}

// ---- tareas ----

// listTasks: abiertas por sección, con las fijadas arriba, luego las que
// tienen fecha (la más próxima primero) y al final las demás por antigüedad.
func (a *App) listTasks(listID int, done bool) ([]Task, error) {
	where, order, limit := `t.done_at IS NULL`,
		`t.pinned DESC, (t.due_on IS NULL), t.due_on ASC, t.created_at ASC, t.id ASC`, 500
	if done {
		where, order, limit = `t.done_at IS NOT NULL`, `t.done_at DESC, t.id DESC`, 50
	}
	return a.queryTasks(fmt.Sprintf(`SELECT %s FROM tasks t JOIN lists l ON l.id = t.list_id
		WHERE t.list_id = $1 AND %s ORDER BY %s LIMIT %d`, taskCols, where, order, limit), listID)
}

func (a *App) countDone(listID int) (n int) {
	a.db.QueryRow(`SELECT count(*) FROM tasks WHERE list_id = $1 AND done_at IS NOT NULL`, listID).Scan(&n)
	return n
}

func (a *App) getTask(id int) (Task, error) {
	ts, err := a.queryTasks(fmt.Sprintf(`SELECT %s FROM tasks t JOIN lists l ON l.id = t.list_id
		WHERE t.id = $1`, taskCols), id)
	if err != nil {
		return Task{}, err
	}
	if len(ts) == 0 {
		return Task{}, sql.ErrNoRows
	}
	return ts[0], nil
}

// agenda: todo lo abierto con fecha hasta dentro de una semana, de todas las
// secciones. El handler lo parte en vencidas / hoy / próximas.
func (a *App) agenda() ([]Task, error) {
	return a.queryTasks(fmt.Sprintf(`SELECT %s FROM tasks t JOIN lists l ON l.id = t.list_id
		WHERE t.done_at IS NULL AND t.due_on IS NOT NULL AND t.due_on <= $1::date + 7
		ORDER BY t.due_on ASC, t.pinned DESC, t.id ASC`, taskCols), a.todayStr())
}

// dueCount: vencidas o para hoy, el número del globito en el encabezado.
func (a *App) dueCount() (n int) {
	a.db.QueryRow(`SELECT count(*) FROM tasks
		WHERE done_at IS NULL AND due_on IS NOT NULL AND due_on <= $1::date`, a.todayStr()).Scan(&n)
	return n
}

func (a *App) searchTasks(q string) ([]Task, error) {
	// búsqueda simple sin acentos ni ranking: es una lista personal, no un
	// buscador. Los abiertos primero, luego los hechos más recientes.
	pat := "%" + strings.ToLower(q) + "%"
	return a.queryTasks(fmt.Sprintf(`SELECT %s FROM tasks t JOIN lists l ON l.id = t.list_id
		WHERE lower(t.title) LIKE $1 OR lower(t.notes) LIKE $1
		ORDER BY (t.done_at IS NOT NULL), t.done_at DESC, t.created_at DESC LIMIT 100`, taskCols), pat)
}

func (a *App) queryTasks(q string, args ...any) ([]Task, error) {
	rows, err := a.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.ListID, &t.ListName, &t.Title, &t.Notes,
			&t.DueOn, &t.Pinned, &t.DoneAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		a.decorate(&t)
		out = append(out, t)
	}
	return out, rows.Err()
}

// decorate traduce la fecha límite a una etiqueta legible ("hoy", "mañana",
// "vencida · 3 mar") y a un estado que el CSS pinta con color.
func (a *App) decorate(t *Task) {
	if !t.DueOn.Valid {
		return
	}
	days := daysBetween(a.today(), t.DueOn.Time)
	switch {
	case days < 0:
		t.DueState, t.DueLabel = "over", "venció "+fmtDate(t.DueOn.Time)
	case days == 0:
		t.DueState, t.DueLabel = "today", "hoy"
	case days == 1:
		t.DueState, t.DueLabel = "soon", "mañana"
	case days <= 6:
		t.DueState, t.DueLabel = "soon", spanishDays[int(t.DueOn.Time.Weekday())]
	default:
		t.DueState, t.DueLabel = "", fmtDate(t.DueOn.Time)
	}
}

// daysBetween cuenta días de calendario entre dos fechas ignorando husos:
// el "date" de Postgres llega a medianoche UTC y hoy vive en la zona de la
// app, así que solo comparamos año/mes/día.
func daysBetween(from, to time.Time) int {
	y1, m1, d1 := from.Date()
	y2, m2, d2 := to.Date()
	a := time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)
	b := time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours() / 24)
}

func (a *App) todayStr() string { return a.today().Format("2006-01-02") }

// ---- formatos ----

var spanishMonths = [...]string{"", "enero", "febrero", "marzo", "abril", "mayo", "junio",
	"julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

var spanishDays = [...]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}

// fmtDate: "3 mar" (o "3 mar 2025" si es de otro año).
func (a *App) fmtDateY(t time.Time) string {
	if t.In(a.loc).Year() != time.Now().In(a.loc).Year() {
		return fmt.Sprintf("%d %s %d", t.Day(), spanishMonths[int(t.Month())][:3], t.Year())
	}
	return fmtDate(t)
}

func fmtDate(t time.Time) string {
	return fmt.Sprintf("%d %s", t.Day(), spanishMonths[int(t.Month())][:3])
}

// fmtWhen: "3 de marzo de 2026, 14:05" para el detalle de la tarea.
func (a *App) fmtWhen(t time.Time) string {
	t = t.In(a.loc)
	return fmt.Sprintf("%d de %s de %d, %02d:%02d",
		t.Day(), spanishMonths[int(t.Month())], t.Year(), t.Hour(), t.Minute())
}
