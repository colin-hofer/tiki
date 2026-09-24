package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"unicode"

	"tiki/internal/tiki"
)

func (a *app) print(out any) error {
	if !a.json {
		switch value := out.(type) {
		case tiki.Item:
			_, err := fmt.Fprintf(a.out, "%s  %s\n%s · %s · priority %g · version %d\nAssignees: %v\nTags: %s\n", value.ID, terminalText(value.Title), terminalText(string(value.Type)), terminalText(string(value.Status)), value.Priority, value.Version, value.Assignees, terminalText(strings.Join(value.Tags, ", ")))
			if err != nil {
				return err
			}
			if value.Description != "" {
				_, err = fmt.Fprintln(a.out, "\n"+terminalText(value.Description))
			}
			return err
		case tiki.Page:
			w := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(w, "ID\tSTATUS\tPRIORITY\tTITLE\tASSIGNEES\tTAGS"); err != nil {
				return err
			}
			for _, i := range value.Items {
				if _, err := fmt.Fprintf(w, "%s\t%s\t%g\t%s\t%v\t%s\n", i.ID, terminalText(string(i.Status)), i.Priority, terminalText(i.Title), i.Assignees, terminalText(strings.Join(i.Tags, ","))); err != nil {
					return err
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if value.NextCursor != "" {
				fmt.Fprintln(a.err, "Next page: --cursor", value.NextCursor)
			}
			return nil
		}
	}
	e := json.NewEncoder(a.out)
	if !a.json {
		e.SetIndent("", "  ")
	}
	return e.Encode(out)
}

func terminalText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return '\uFFFD'
		}
		return r
	}, s)
}
