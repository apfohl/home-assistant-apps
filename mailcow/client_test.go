package main

import (
	"reflect"
	"testing"
)

func TestParseTableColumns(t *testing.T) {
	page := `
<table id="aliastable">
  <thead>
    <tr><th>Alias address</th><th>Goto</th><th>Active</th></tr>
  </thead>
  <tbody>
    <tr>
      <td>alias@example.com</td>
      <td>user1@example.com<br>user2@example.com</td>
      <td>yes</td>
    </tr>
    <tr>
      <td>single@example.com</td>
      <td>user@example.com</td>
      <td>yes</td>
    </tr>
    <tr>
      <td>-</td>
      <td></td>
      <td>no</td>
    </tr>
  </tbody>
</table>
`

	rows, err := parseTableColumns(page, "aliastable", 2)
	if err != nil {
		t.Fatalf("parseTableColumns: %v", err)
	}

	want := [][]string{
		{"alias@example.com", "user1@example.com user2@example.com"},
		{"single@example.com", "user@example.com"},
	}

	if !reflect.DeepEqual(rows, want) {
		t.Errorf("parseTableColumns = %v, want %v", rows, want)
	}
}

func TestParseTableColumnsEmpty(t *testing.T) {
	page := `<table id="aliastable"><tbody></tbody></table>`

	rows, err := parseTableColumns(page, "aliastable", 2)
	if err != nil {
		t.Fatalf("parseTableColumns: %v", err)
	}

	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}
