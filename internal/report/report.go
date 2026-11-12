// Package report renders session analysis as ASCII tables.
package report

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"agentlens/internal/lens"
)

func value(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.3f s", *v)
}
func number[T ~int | ~int64](v *T) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprint(*v)
}

// ASCII borders; Unicode text is measured in runes so Cyrillic stays aligned.
func wrapCell(s string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	out := []string{}
	line := ""
	for _, word := range strings.Fields(s) {
		if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > width {
			out = append(out, line)
			line = ""
		}
		for utf8.RuneCountInString(word) > width {
			r := []rune(word)
			out = append(out, string(r[:width]))
			word = string(r[width:])
		}
		if word != "" {
			if line != "" {
				line += " "
			}
			line += word
		}
	}
	if line != "" || len(out) == 0 {
		out = append(out, line)
	}
	return out
}
func table(w io.Writer, headers []string, rows [][]string) {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, s)
	}
	data := make([][]string, 0, len(rows)+1)
	data = append(data, headers)
	data = append(data, rows...)
	widths := make([]int, len(headers))
	for _, row := range data {
		for i, c := range row {
			n := utf8.RuneCountInString(clean(c))
			if n > 52 {
				n = 52
			}
			if n > widths[i] {
				widths[i] = n
			}
		}
	}
	border := func() {
		fmt.Fprint(w, "+")
		for _, n := range widths {
			fmt.Fprint(w, strings.Repeat("-", n+2), "+")
		}
		fmt.Fprintln(w)
	}
	border()
	for rowIndex, row := range data {
		cells := make([][]string, len(headers))
		height := 1
		for i, c := range row {
			cells[i] = wrapCell(clean(c), widths[i])
			if len(cells[i]) > height {
				height = len(cells[i])
			}
		}
		for line := 0; line < height; line++ {
			fmt.Fprint(w, "|")
			for i, n := range widths {
				v := ""
				if line < len(cells[i]) {
					v = cells[i][line]
				}
				fmt.Fprint(w, " ", v, strings.Repeat(" ", n-utf8.RuneCountInString(v)), " |")
			}
			fmt.Fprintln(w)
		}

		if rowIndex == 0 {
			border()
		}
	}
	border()
}
func printTools(w io.Writer, groups []lens.Group) {
	codex, mcp := [][]string{}, [][]string{}
	for _, g := range groups {
		row := []string{g.Tool, fmt.Sprint(g.Calls), fmt.Sprint(g.Success), fmt.Sprint(g.Failure), fmt.Sprint(g.Unknown), fmt.Sprint(g.Pending), fmt.Sprintf("%d/%d", g.Measured, g.Calls), value(g.Total), value(g.P50), value(g.P95)}
		if server := lens.MCPServer(g.Tool); server != "" {
			row[0] = strings.TrimPrefix(strings.TrimPrefix(g.Tool, "functions."), "mcp__"+server+"__")
			mcp = append(mcp, append([]string{server}, row...))
		} else {
			codex = append(codex, row)
		}
	}
	headers := []string{"Тул", "Вызовы", "OK", "ERR", "?", "WAIT", "Измерено", "Сумма", "p50", "p95"}
	fmt.Fprintln(w, "\nCODEX | внешние вызовы")
	if len(codex) == 0 {
		codex = [][]string{{"(нет записей)", "-", "-", "-", "-", "-", "-", "-", "-", "-"}}
	}
	table(w, headers, codex)
	fmt.Fprintln(w, "\nMCP TOOLS | внешние вызовы")
	if len(mcp) == 0 {
		mcp = [][]string{{"(нет записей)", "-", "-", "-", "-", "-", "-", "-", "-", "-", "-"}}
	}
	table(w, append([]string{"Сервер"}, headers...), mcp)
}

// Print writes the human-readable report.
func Print(w io.Writer, r lens.Report, details bool) {
	fmt.Fprintln(w, "exec — один запуск скрипта; внутри может быть несколько операций. OK относится только к оболочке.")
	if len(r.Sessions) != 1 || len(r.Errors) > 0 {
		table(w, []string{"Сессии", "Ошибки файлов", "Полнота"}, [][]string{{fmt.Sprint(r.SessionCount), fmt.Sprint(len(r.Errors)), fmt.Sprint(r.Complete)}})
	}
	if len(r.Sessions) > 1 {
		fmt.Fprintln(w, "\nALL SESSIONS")
		printTools(w, r.Groups)
	}
	for i, s := range r.Sessions {
		fmt.Fprintf(w, "\nSESSION %d | %s\n", i+1, filepath.Base(s.File))
		inspected, processes, failed := 0, 0, 0
		for _, e := range s.Calls {
			if e.OutputInspected {
				inspected++
			}
			for _, p := range e.ProcessResults {
				processes++
				if p.ExitCode != nil && *p.ExitCode != 0 {
					failed++
				}
			}
		}
		var tokens lens.Tokens
		if s.Metrics.Tokens != nil {
			tokens = *s.Metrics.Tokens
		}
		table(w, []string{"Сводка сессии", "Значение"}, [][]string{
			{"Внешних вызовов", fmt.Sprint(len(s.Calls))},
			{"Токены всего (вход + выход)", number(tokens.Total)},
			{"Вход / выход", number(tokens.Input) + " / " + number(tokens.Output)},
			{"Из входа: кэш", number(tokens.Cached)},
			{"Время тулов без перекрытий", value(s.Active)},
			{"Результатов прочитано", fmt.Sprintf("%d / %d", inspected, len(s.Calls))},
			{"Результатов процессов / с ошибкой", fmt.Sprintf("%d / %d", processes, failed)},
		})
		printTools(w, s.Groups)
		rows := [][]string{}
		for _, o := range s.Observations {
			lines := []string{}
			for _, n := range o.Lines {
				lines = append(lines, fmt.Sprint(n))
			}
			address := strings.Join(lines, ", ")
			rows = append(rows, []string{o.Fact, o.Action, address})
		}
		fmt.Fprintln(w, "\nНАБЛЮДЕНИЯ")
		table(w, []string{"Факт", "Что проверить", "Строки журнала"}, rows)
		if details {
			if len(s.Metrics.References) > 0 {
				refs := [][]string{}
				for _, ref := range s.Metrics.References {
					refs = append(refs, []string{ref.Tool, fmt.Sprint(ref.Mentions)})
				}
				table(w, []string{"Ссылки в коде (не вызовы)", "Упоминания"}, refs)
			}
			fmt.Fprintf(w, "\nDETAILS | %s | %s\n", s.ID, s.File)
			rows := [][]string{}
			for _, e := range s.Calls {
				end := "?"
				if e.EndLine != nil {
					end = fmt.Sprint(*e.EndLine)
				}
				rows = append(rows, []string{e.Tool, e.CallID, fmt.Sprintf("%d-%s", e.StartLine, end), e.Outcome, value(e.Seconds)})
			}
			table(w, []string{"Тул", "Call ID", "Строки", "Результат", "Время"}, rows)
			warnings := [][]string{}
			for _, warning := range s.Warnings {
				warnings = append(warnings, []string{warning})
			}
			if len(warnings) > 0 {
				table(w, []string{"Предупреждения"}, warnings)
			}
		}
	}
	if r.AI != nil {
		fmt.Fprintln(w, "\nМНЕНИЕ ИИ (интерпретация, не вычисленный факт)")
		text := r.AI.Text
		if r.AI.Error != "" {
			text = r.AI.Error
		}
		table(w, []string{"Модель", "Мнение", "Контекст и ответ"}, [][]string{{r.AI.Model, text, r.AI.Artifact}})
	}
	if len(r.Errors) > 0 {
		rows := [][]string{}
		for _, e := range r.Errors {
			rows = append(rows, []string{e.File, e.Error})
		}
		table(w, []string{"Файл", "Ошибка"}, rows)
	}
}
