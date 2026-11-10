package mist

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func TestReads(t *testing.T) {
	for _, c := range []struct {
		tpl  string
		want map[string][]string
		all  bool
	}{
		{`{{ customer.first_name }} {{ customer["last_name"] }}`, map[string][]string{"customer": {"first_name", "last_name"}}, false},
		{`{{ customer.address.city }}{{ customer.address.zip }}`, map[string][]string{"customer": {"address"}}, false},
		{`{{ customer[customer.attr_name] }}`, map[string][]string{"customer": nil}, false},
		{`{{ customer.attr_name }}{{ customer[customer.attr_name] }}`, map[string][]string{"customer": nil}, false},
		{`{{ customer.size }}`, map[string][]string{"customer": nil}, false},
		{`{% for a in customer %}{{ a }}{% endfor %}`, map[string][]string{"customer": nil, "a": nil}, false},
		{`{{ customer | json }}`, map[string][]string{"customer": nil}, false},
		{`{% assign c = customer %}{{ c.x }}`, map[string][]string{"customer": nil, "c": {"x"}}, false},
		{`{% if false %}{{ snippets.footer }}{% else %}{{ snippets.header }}{% endif %}`, map[string][]string{"snippets": {"footer", "header"}}, false},
		{`{{ items | where_exp: "i", "i.id == customer.pick" }}`, map[string][]string{"items": nil, "i": {"id"}, "customer": {"pick"}}, false},
		{`{% for i in (1..customer.n) %}{{ forloop.index }}{% endfor %}`, map[string][]string{"customer": {"n"}}, false},
		{`{% increment n %}`, map[string][]string{"n": nil}, false},
		{`{% liquid
echo customer.a
%}`, map[string][]string{"customer": {"a"}}, false},
		{`{% t %}{{ customer.a }}`, map[string][]string{"customer": {"a"}}, true},
		{`plain`, map[string][]string{}, false},
	} {
		e := Engine{Tags: map[string]TagFunc{"t": func(dst []byte, _ Tag) ([]byte, error) { return dst, nil }}}
		got, all, err := e.Reads(c.tpl)
		if err != nil || all != c.all || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %#v, %v, %v; want %#v, %v", c.tpl, got, all, err, c.want, c.all)
		}
	}
	if keys, _, err := (Engine{}).Reads("{{ x | nope }}"); err == nil || keys != nil {
		t.Errorf("got %v, %v; want Check's error", keys, err)
	}
}

// TestReadsSound renders every case and corpus template with its data pruned to what
// Reads reports, which must give the same result as the full data.
func TestReadsSound(t *testing.T) {
	type tc struct {
		name, tpl string
		data      map[string]any
	}
	var cs []tc
	for _, c := range loadCases(t) {
		cs = append(cs, tc{c.Name, c.Tpl, c.Data})
	}
	path := "testdata/corpus.json"
	if p := os.Getenv("MIST_CORPUS"); p != "" {
		path = p
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var corpus []corpusCase
	if err := json.Unmarshal(b, &corpus); err != nil {
		t.Fatal(err)
	}
	for _, c := range corpus {
		cs = append(cs, tc{c.Name, c.Tpl, c.Data})
	}
	var pruned int
	for _, c := range cs {
		keys, all, err := corpusEngine.Reads(c.tpl)
		if err != nil || all {
			continue
		}
		data := prune(c.data, keys)
		if !reflect.DeepEqual(data, c.data) {
			pruned++
		}
		for _, strict := range []bool{false, true} {
			want, werr := corpusEngine.Render(c.tpl, c.data, strict)
			got, gerr := corpusEngine.Render(c.tpl, data, strict)
			if got != want || fmt.Sprint(gerr) != fmt.Sprint(werr) {
				t.Errorf("%s (strict %v): %q\nreads %v\npruned: %q, %v\nfull:   %q, %v", c.name, strict, c.tpl, keys, got, gerr, want, werr)
			}
		}
	}
	t.Logf("%d of %d templates had data pruned", pruned, len(cs))
}

func prune(data map[string]any, keys map[string][]string) map[string]any {
	if data == nil {
		return nil
	}
	out := map[string]any{}
	for name, ks := range keys {
		v, ok := data[name]
		if !ok {
			continue
		}
		m, isMap := v.(map[string]any)
		if ks == nil || !isMap {
			out[name] = v
			continue
		}
		sub := map[string]any{}
		for _, k := range ks {
			if x, ok := m[k]; ok {
				sub[k] = x
			}
		}
		out[name] = sub
	}
	return out
}
