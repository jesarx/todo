package main

import (
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"
)

// Las notas de una tarea aceptan un formato mínimo, del tipo que uno escribe
// sin pensar:
//
//	* punto            → lista con viñetas (también - y +)
//	1. punto           → lista numerada
//	[ ] punto          → casilla (se marca con un toque, sin abrir la nota)
//	[x] punto          → casilla marcada
//	**negritas**, _cursivas_, `código`, y las ligas se vuelven enlaces
//
// Todo se escapa antes de tocarlo: las notas nunca pueden inyectar HTML.

var (
	reCheck  = regexp.MustCompile(`^(?:[-*+•]\s+)?\[([ xX])\]\s*(.*)$`)
	reBullet = regexp.MustCompile(`^[-*+•]\s+(.*)$`)
	reNumber = regexp.MustCompile(`^\d{1,3}[.)]\s+(.*)$`)
	reBold   = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	reItalic = regexp.MustCompile(`_([^_\n]+)_`)
	reCode   = regexp.MustCompile("`([^`\n]+)`")
	reLink   = regexp.MustCompile(`https?://[^\s<]+[^\s<.,;:!?)\]]`)
)

// blockKind distingue el tipo de lista abierta para saber cuándo cerrarla.
type blockKind int

const (
	blockNone blockKind = iota
	blockUL
	blockOL
	blockCheck
)

func renderNotes(taskID int, src string) template.HTML {
	src = strings.ReplaceAll(src, "\x00", "")
	src = strings.ReplaceAll(src, "\r\n", "\n")

	var b strings.Builder
	var para []string
	open := blockNone
	checks := 0 // índice de casilla dentro de la nota (el que recibe /chk)

	closeBlock := func() {
		switch open {
		case blockUL, blockCheck:
			b.WriteString("</ul>")
		case blockOL:
			b.WriteString("</ol>")
		}
		open = blockNone
	}
	flushPara := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + strings.Join(para, "<br>") + "</p>")
			para = para[:0]
		}
	}
	openBlock := func(k blockKind) {
		if open == k {
			return
		}
		closeBlock()
		switch k {
		case blockUL:
			b.WriteString("<ul>")
		case blockOL:
			b.WriteString("<ol>")
		case blockCheck:
			b.WriteString(`<ul class="checks">`)
		}
		open = k
	}

	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			flushPara()
			closeBlock()
			continue
		}
		if m := reCheck.FindStringSubmatch(line); m != nil {
			flushPara()
			openBlock(blockCheck)
			b.WriteString(checkItem(taskID, checks, m[1] != " ", inline(m[2])))
			checks++
			continue
		}
		if m := reBullet.FindStringSubmatch(line); m != nil {
			flushPara()
			openBlock(blockUL)
			b.WriteString("<li>" + inline(m[1]) + "</li>")
			continue
		}
		if m := reNumber.FindStringSubmatch(line); m != nil {
			flushPara()
			openBlock(blockOL)
			b.WriteString("<li>" + inline(m[1]) + "</li>")
			continue
		}
		closeBlock()
		para = append(para, inline(line))
	}
	flushPara()
	closeBlock()
	return template.HTML(b.String())
}

// checkItem: la casilla es un formulario propio para que se pueda marcar
// desde la lista sin abrir la tarea (y desde la cola offline). Manda el
// estado deseado, no un "invierte", así reintentarlo nunca lo revierte.
func checkItem(taskID, idx int, done bool, text string) string {
	cls, glyph, set := "", "○", "1"
	if done {
		cls, glyph, set = " done", "✓", "0"
	}
	if taskID == 0 { // sin tarea todavía (vista previa): casilla decorativa
		return `<li class="chk` + cls + `"><span class="box">` + glyph + `</span><span>` + text + `</span></li>`
	}
	id := strconv.Itoa(taskID)
	return `<li class="chk` + cls + `">` +
		`<form method="post" action="/t/` + id + `/chk" class="inline" data-offline>` +
		`<input type="hidden" name="i" value="` + strconv.Itoa(idx) + `">` +
		`<input type="hidden" name="set" value="` + set + `">` +
		`<button class="box" aria-label="Marcar punto">` + glyph + `</button>` +
		`</form><span>` + text + `</span></li>`
}

// inline aplica el formato de renglón. Escapa primero y aparta el código y
// las ligas en marcadores para que negritas y cursivas no se metan dentro de
// una URL (donde _ y * son caracteres normales).
func inline(s string) string {
	s = html.EscapeString(s)
	var saved []string
	protect := func(re *regexp.Regexp, wrap func(string) string) {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			saved = append(saved, wrap(m))
			return "\x00" + strconv.Itoa(len(saved)-1) + "\x00"
		})
	}
	protect(reCode, func(m string) string {
		return "<code>" + strings.Trim(m, "`") + "</code>"
	})
	protect(reLink, func(m string) string {
		return `<a href="` + m + `" target="_blank" rel="noopener noreferrer">` + m + `</a>`
	})
	s = reBold.ReplaceAllString(s, "<strong>$1</strong>")
	s = reItalic.ReplaceAllString(s, "<em>$1</em>")
	for i, v := range saved {
		s = strings.Replace(s, "\x00"+strconv.Itoa(i)+"\x00", v, 1)
	}
	return s
}

// toggleCheck marca o desmarca la casilla número idx de una nota y devuelve
// el texto actualizado. Si el índice no existe, la nota no cambia.
func toggleCheck(notes string, idx int, done bool) string {
	lines := strings.Split(strings.ReplaceAll(notes, "\r\n", "\n"), "\n")
	n := 0
	for i, ln := range lines {
		m := reCheck.FindStringSubmatch(strings.TrimSpace(ln))
		if m == nil {
			continue
		}
		if n == idx {
			mark := " "
			if done {
				mark = "x"
			}
			// conserva la sangría y el guion de lista si los traía
			prefix := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
			body := strings.TrimSpace(ln)
			if b := reBullet.FindStringSubmatch(body); b != nil {
				prefix += body[:len(body)-len(b[1])]
				body = b[1]
			}
			if rest := reCheck.FindStringSubmatch(body); rest != nil {
				lines[i] = prefix + "[" + mark + "] " + rest[2]
			}
			break
		}
		n++
	}
	return strings.Join(lines, "\n")
}
