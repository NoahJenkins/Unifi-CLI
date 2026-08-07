package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"
)

func WriteTable(w io.Writer, headers []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(safeCells(headers), "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(safeCells(row), "\t"))
	}
	return tw.Flush()
}

func safeCells(cells []string) []string {
	result := make([]string, len(cells))
	for i, cell := range cells {
		result[i] = SafeText(cell)
	}
	return result
}

// SafeText makes terminal control characters visible while preserving normal
// printable Unicode text.
func SafeText(value string) string {
	var result strings.Builder
	for _, r := range value {
		if !unicode.IsControl(r) {
			result.WriteRune(r)
			continue
		}
		quoted := strconv.QuoteRuneToGraphic(r)
		result.WriteString(quoted[1 : len(quoted)-1])
	}
	return result.String()
}
