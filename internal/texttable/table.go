package texttable

import (
	"fmt"
	"io"
	"strings"
)

type Column struct {
	Header     string
	AlignRight bool
}

func Render(w io.Writer, columns []Column, rows [][]string) {
	if len(columns) == 0 {
		return
	}

	widths := make([]int, len(columns))
	headers := make([]string, len(columns))
	for i, col := range columns {
		headers[i] = col.Header
		widths[i] = len(col.Header)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				break
			}
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	printRow(w, columns, headers, widths)
	printSeparator(w, widths)
	for _, row := range rows {
		printRow(w, columns, row, widths)
	}
}

func printRow(w io.Writer, columns []Column, cells []string, widths []int) {
	for i, col := range columns {
		if i > 0 {
			fmt.Fprint(w, "  ")
		}
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		if col.AlignRight {
			fmt.Fprintf(w, "%*s", widths[i], cell)
			continue
		}
		fmt.Fprintf(w, "%-*s", widths[i], cell)
	}
	fmt.Fprintln(w)
}

func printSeparator(w io.Writer, widths []int) {
	for i, width := range widths {
		if i > 0 {
			fmt.Fprint(w, "  ")
		}
		fmt.Fprint(w, strings.Repeat("-", width))
	}
	fmt.Fprintln(w)
}
