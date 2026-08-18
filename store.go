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
	Pinned    bool
	DoneAt    sql.NullTime
	CreatedAt time.Time
}

// Column es una sección con sus pendientes, para la vista de todas.
type Column struct {
	List  List
	Tasks []Task
}

const taskCols = `t.id, t.list_id, l.name, t.title, t.notes, t.pinned, t.done_at, t.created_at`

// orden de los pendientes: primero las fijadas y luego lo más reciente
// arriba, que es lo que uno acaba de anotar y trae en la cabeza
const openOrder = `t.pinned DESC, t.created_at DESC, t.id DESC`

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

// listTasks: las de una sección, abiertas o hechas.
func (a *App) listTasks(listID int, done bool) ([]Task, error) {
	where, order, limit := `t.done_at IS NULL`, openOrder, 500
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

// board: todos los pendientes de todas las secciones, ya agrupados y en el
// orden de las pestañas. Una sola consulta; agrupar en Go sale más barato
// que una consulta por sección.
func (a *App) board() ([]Column, int, error) {
	lists, err := a.listLists()
	if err != nil {
		return nil, 0, err
	}
	tasks, err := a.queryTasks(fmt.Sprintf(`SELECT %s FROM tasks t JOIN lists l ON l.id = t.list_id
		WHERE t.done_at IS NULL ORDER BY l.position, l.id, %s`, taskCols, openOrder))
	if err != nil {
		return nil, 0, err
	}
	cols := make([]Column, len(lists))
	at := make(map[int]int, len(lists))
	for i, l := range lists {
		cols[i] = Column{List: l}
		at[l.ID] = i
	}
	for _, t := range tasks {
		if i, ok := at[t.ListID]; ok {
			cols[i].Tasks = append(cols[i].Tasks, t)
		}
	}
	return cols, len(tasks), nil
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
			&t.Pinned, &t.DoneAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- formatos ----

var spanishMonths = [...]string{"", "enero", "febrero", "marzo", "abril", "mayo", "junio",
	"julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

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
