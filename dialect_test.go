package mist

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

var errToy = errors.New("toy")

type call struct {
	op   string
	a, b any
}

// toyDialect prints values with type prefixes and compares by their printed form,
// deliberately unlike liquidjs, so the tests prove the hooks are used.
func toyDialect(calls *[]call) *Dialect {
	return &Dialect{
		Output: func(dst []byte, v any) ([]byte, error) {
			switch x := v.(type) {
			case bool:
				if x {
					return append(dst, "yes"...), nil
				}
				return append(dst, "no"...), nil
			case int64:
				return fmt.Appendf(dst, "int:%d", x), nil
			case float64:
				return fmt.Appendf(dst, "float:%g", x), nil
			case json.Number:
				return fmt.Appendf(dst, "num:%s", x), nil
			case []any:
				return fmt.Appendf(dst, "list:%d", len(x)), nil
			case map[string]any:
				return nil, ErrUnsupported
			case Keyword:
				return fmt.Appendf(dst, "kw:%s", x), nil
			}
			return nil, errToy
		},
		Compare: func(op string, a, b any) (bool, error) {
			*calls = append(*calls, call{op, a, b})
			switch op {
			case "==":
				return fmt.Sprint(a) == fmt.Sprint(b), nil
			case "!=":
				return fmt.Sprint(a) != fmt.Sprint(b), nil
			}
			return false, ErrUnsupported
		},
	}
}

func TestDialectOutput(t *testing.T) {
	e := Engine{Dialect: toyDialect(new([]call))}
	vars := map[string]any{"t": true, "f": false, "n": 2.5, "j": json.Number("2.0"), "xs": []any{1, 2}, "s": "str", "m": map[string]any{}, "odd": int32(1)}
	out, err := e.Render("{{ t }}{{ f }}{{ 3 }}{{ n }}{{ j }}{{ xs }}{{ empty }}|{{ s }}{{ missing }}{{ 'lit' }}", vars, false)
	if want := "yesnoint:3float:2.5num:2.0list:2kw:empty|strlit"; err != nil || out != want {
		t.Fatalf("got %q, %v; want %q", out, err, want)
	}
	if _, err := e.Render("{{ m }}", vars, false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("map: got %v; want ErrUnsupported", err)
	}
	if _, err := e.Render("{{ odd }}", vars, false); !errors.Is(err, errToy) {
		t.Errorf("odd type: got %v; want the dialect's error", err)
	}
	// echo and raw skip liquidjs's outputEscape, which the Output hook stands in for.
	for _, tpl := range []string{"{% echo t %}", "{% echo empty %}", "{{ t | raw }}"} {
		if _, err := e.Render(tpl, vars, false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: got %v; want ErrUnsupported", tpl, err)
		}
	}
}

func TestDialectCompare(t *testing.T) {
	var calls []call
	e := Engine{Dialect: toyDialect(&calls)}
	vars := map[string]any{"n": 2.5, "s": "", "z": nil}
	for tpl, want := range map[string]string{
		"{% if n == '2.5' %}Y{% else %}N{% endif %}":            "Y", // liquidjs: different types, never equal
		"{% if n != 2 and s == blank %}Y{% else %}N{% endif %}": "N",
		"{% if n %}Y{% endif %}":                                "Y", // truthiness isn't a comparison
	} {
		if out, err := e.Render(tpl, vars, false); err != nil || out != want {
			t.Errorf("%s: got %q, %v; want %q", tpl, out, err, want)
		}
	}

	calls = nil
	if _, err := e.Render("{% if missing == z %}{% endif %}{% if 3 == s %}{% endif %}{% if s == blank %}{% endif %}{% if z == nil %}{% endif %}{% if s != empty %}{% endif %}", vars, true); err != nil {
		t.Fatal(err)
	}
	want := []call{{"==", nil, nil}, {"==", int64(3), ""}, {"==", "", Blank}, {"==", nil, nil}, {"!=", "", Empty}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("hook arguments: got %#v; want %#v", calls, want)
	}
	for _, tpl := range []string{"{% if 3 < 4 %}{% endif %}", "{% if s contains 'a' %}{% endif %}"} {
		if _, err := e.Render(tpl, vars, false); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: got %v; want ErrUnsupported", tpl, err)
		}
	}
	// Ordering against a keyword goes to the hook too.
	calls = nil
	if _, err := e.Render("{% if s < blank %}{% endif %}", vars, false); !errors.Is(err, ErrUnsupported) || !reflect.DeepEqual(calls, []call{{"<", "", Blank}}) {
		t.Errorf("s < blank: got %v, %#v; want ErrUnsupported from the hook", err, calls)
	}
}

func TestDialectReject(t *testing.T) {
	for c, tpls := range map[Constructs][]string{
		TrimMarkers:       {"a {{- x }}", "{{ x -}} b", "{%- if true %}{% endif %}", "{% comment %}x{%- endcomment %}"},
		NegativeLiterals:  {"{{ -3 }}", "{{ xs[-1] }}", "{% if false %}{{ -1 }}{% endif %}"},
		UnspacedOperators: {"{% if x==2 %}{% endif %}", "{% if x== 2 %}{% endif %}", "{% if false %}{% if a<b %}{% endif %}{% endif %}"},
		RawBlocks:         {"{% raw %}{{ x }}{% endraw %}"},
		BlankKeyword:      {"{% if x == blank %}{% endif %}"},
		EmptyKeyword:      {"{% if x == empty %}{% endif %}"},
		LooseExpressions:  {"{% if a b %}{% endif %}", "{% if not a %}{% endif %}", "{% if a == %}{% endif %}", "{% if a == b == c %}{% endif %}", "{% if a and %}{% endif %}", "{% if a and b c %}{% endif %}", "{{ a and b }}", "{{ a b }}", "{% assign x = a == 1 %}", "{% echo a or b %}", "{% if false %}{{ and }}{% endif %}"},
	} {
		strict := Engine{Dialect: &Dialect{Reject: c}}
		for _, tpl := range tpls {
			if err := strict.Check(tpl); !errors.Is(err, ErrUnsupported) {
				t.Errorf("%s with Reject %b: Check got %v; want ErrUnsupported", tpl, c, err)
			}
			if _, err := strict.Render(tpl, map[string]any{"xs": []any{1}}, false); !errors.Is(err, ErrUnsupported) {
				t.Errorf("%s with Reject %b: Render got %v; want ErrUnsupported", tpl, c, err)
			}
			if err := Check(tpl); err != nil {
				t.Errorf("%s without a dialect: %v", tpl, err)
			}
		}
	}
	if err := (Engine{Dialect: &Dialect{Reject: UnspacedOperators}}).Check("{% if x ==2 %}{% endif %}"); err != nil {
		t.Errorf("whitespace before the operator is enough: %v", err)
	}
	if err := (Engine{Dialect: &Dialect{Reject: LooseExpressions}}).Check("{% if a == 1 and b or c contains 'x' | default: true %}{% endif %}{{ a | upcase }}{% assign x = a %}{% echo a %}"); err != nil {
		t.Errorf("cond's grammar and lone operands: %v", err)
	}
}

func TestDialectNoDefaultLeniency(t *testing.T) {
	e := Engine{Dialect: &Dialect{NoDefaultLeniency: true}}
	if _, err := e.Render("{{ x | default: 'd' }}", nil, true); !errors.Is(err, ErrUndefined) {
		t.Errorf("strict leading default: got %v; want ErrUndefined", err)
	}
	if out, err := e.Render("{{ x | default: 'd' }}", nil, false); err != nil || out != "d" {
		t.Errorf("lax: got %q, %v; want d", out, err)
	}
}

func TestDialectSeesFractionalNumbers(t *testing.T) {
	e := Engine{Dialect: toyDialect(new([]call))}
	out, err := e.Render("{{ 1.5 }} {{ -2.5 | abs }} {{ 2 | times: 1 }} {{ 7 | divided_by: 2 }}", nil, false)
	if want := "float:1.5 float:2.5 int:2 float:3.5"; err != nil || out != want {
		t.Fatalf("got %q, %v; want %q", out, err, want)
	}
}

// NumberText keeps a literal's text, so a dialect can tell 2.0 from 2.
func TestDialectNumberText(t *testing.T) {
	var seen []any
	e := Engine{
		Dialect: &Dialect{NumberText: true, Output: func(dst []byte, v any) ([]byte, error) {
			seen = append(seen, v)
			return fmt.Appendf(dst, "%v", v), nil
		}},
		Filters: map[string]FilterFunc{"arg": func(f Filter) (any, error) { seen = append(seen, f.Args[0]); return f.Input, nil }},
	}
	out, err := e.Render("{{ 2.0 }}|{{ -3 }}|{{ 'x' | arg: 1.50 }}|{% assign n = 2.0 %}{{ n }}|{{ 1 | plus: 1 }}", nil, false)
	if want := "2.0|-3|x|2.0|2"; err != nil || out != want {
		t.Fatalf("got %q, %v; want %q", out, err, want)
	}
	if want := []any{json.Number("2.0"), json.Number("-3"), json.Number("1.50"), json.Number("2.0"), int64(2)}; !reflect.DeepEqual(seen, want) {
		t.Errorf("hooks saw %#v; want %#v", seen, want)
	}
}

// NoBuiltinFilters leaves only registered filters.
func TestDialectNoBuiltinFilters(t *testing.T) {
	e := Engine{
		Dialect: &Dialect{NoBuiltinFilters: true},
		Filters: map[string]FilterFunc{
			"mine": func(Filter) (any, error) { return "m", nil },
			"back": func(Filter) (any, error) { return nil, ErrBuiltin },
		},
	}
	if out, err := e.Render("{{ x | mine }}", nil, false); err != nil || out != "m" {
		t.Errorf("registered: got %q, %v", out, err)
	}
	for _, tpl := range []string{"{{ x | upcase }}", "{{ x | default: 1 }}", "{{ x | back }}", "{% if x %}{{ y | size }}{% endif %}"} {
		var me *Error
		_, err := e.Render(tpl, nil, false)
		if tpl[len(tpl)-1] == '}' && strings.Contains(tpl, "if x") {
			err = e.Check(tpl) // dead at render time, so only Check sees it
		}
		if !errors.As(err, &me) || !strings.HasPrefix(me.Code, "filter:") {
			t.Errorf("%s: got %v; want a filter: bail", tpl, err)
		}
	}
}

// StrictAssign raises an undefined assign value under strict.
func TestDialectStrictAssign(t *testing.T) {
	tpl := "{% assign x = missing %}[{{ x }}]"
	if out, err := (Engine{}).Render(tpl, nil, true); err != nil || out != "[]" {
		t.Errorf("liquidjs: got %q, %v; want [] (lenient)", out, err)
	}
	e := Engine{Dialect: &Dialect{StrictAssign: true}}
	if _, err := e.Render(tpl, nil, true); !errors.Is(err, ErrUndefined) {
		t.Errorf("StrictAssign: got %v; want ErrUndefined", err)
	}
	if out, err := e.Render(tpl, nil, false); err != nil || out != "[]" {
		t.Errorf("StrictAssign, lax: got %q, %v; want []", out, err)
	}
}

// RubyLookups follows Ruby Liquid's Context#variable; want is Ruby Liquid 2.5's strict
// result, "undefined" for VariableNotFound.
func TestDialectRubyLookups(t *testing.T) {
	e := Engine{Dialect: &Dialect{RubyLookups: true}}
	for _, c := range []struct {
		tpl  string
		vars map[string]any
		want string
	}{
		{"[{{ c.x }}]", map[string]any{"c": nil}, "[]"},
		{"[{{ c.x.y }}]", map[string]any{"c": nil}, "[]"},
		{"[{{ c.x }}]", map[string]any{"c": false}, "[false]"},
		{"[{{ c.p.x }}]", map[string]any{"c": map[string]any{"p": nil}}, "undefined"},
		{"[{{ c.p.x.y }}]", map[string]any{"c": map[string]any{"p": nil}}, "undefined"},
		{"[{{ c.p.q.x }}]", map[string]any{"c": map[string]any{"p": map[string]any{"q": nil}}}, "undefined"},
		{"[{{ c.p[0] }}]", map[string]any{"c": map[string]any{"p": nil}}, "undefined"},
		{"[{{ c.p.x }}]", map[string]any{"c": map[string]any{"p": false}}, "undefined"},
		{"[{{ c.p }}]", map[string]any{"c": map[string]any{"p": nil}}, "[]"},
		{"{% if c.p.x %}y{% else %}n{% endif %}", map[string]any{"c": map[string]any{"p": nil}}, "n"},
		{"[{{ c[0] }}]", map[string]any{"c": []any{}}, "[]"},
		{"[{{ c[3] }}]", map[string]any{"c": []any{1}}, "[]"},
		{"[{{ c[0] }}]", map[string]any{"c": []any{1}}, "[1]"},
		{"[{{ c[i] }}]", map[string]any{"c": []any{}, "i": json.Number("2")}, "[]"},
		{"[{{ c[i] }}]", map[string]any{"c": []any{1}, "i": int64(-1)}, "[1]"},
		{"[{{ c[i] }}]", map[string]any{"c": []any{1}, "i": int64(-2)}, "undefined"},
		{"[{{ c[i] }}]", map[string]any{"c": []any{}, "i": json.Number("0.0")}, "undefined"},
		{"[{{ c[0].x }}]", map[string]any{"c": []any{}}, "undefined"},
		{"[{{ c[0] }}]", map[string]any{"c": map[string]any{"0": "z"}}, "[]"},
		{"[{{ c[i] }}]", map[string]any{"c": map[string]any{}, "i": 1}, "[]"},
		{"[{{ c['0'] }}]", map[string]any{"c": map[string]any{"0": "z"}}, "[z]"},
		{"[{{ c[1.5] }}]", map[string]any{"c": map[string]any{}}, "undefined"},
		{"[{{ c[i] }}]", map[string]any{"c": map[string]any{"0": "z"}, "i": "0"}, "[z]"},
		// strings' size, first and last count characters, as render's legacy engine does
		{"[{{ s.first }}{{ s.last }}{{ s.size }}][{{ e.first }}{{ e.last }}{{ e.size }}][{{ s.first.last }}]", map[string]any{"s": "é😀x", "e": ""}, "[éx3][0][é]"},
		{"{% if e.first == '' %}y{% endif %}", map[string]any{"e": ""}, "y"},
		{"[{{ h.size }}{{ h.first }}{{ xs.first }}{{ xs.last }}{{ xs.size }}]", map[string]any{"h": map[string]any{"first": "F", "k": 1}, "xs": []any{1, 2}}, "[2F122]"},
		// only a property computes them, and an Array has no string keys
		{"[{{ xs['first'] }}]", map[string]any{"xs": []any{1}}, "undefined"},
		{"[{{ xs['0'] }}]", map[string]any{"xs": []any{1}}, "undefined"},
		{"[{{ xs.n }}]", map[string]any{"xs": []any{1}}, "undefined"},
		{"[{{ h['size'] }}]", map[string]any{"h": map[string]any{}}, "undefined"},
		{"[{{ h.last }}]", map[string]any{"h": map[string]any{"k": 1}}, "undefined"},
		{"[{{ s[k] }}]", map[string]any{"s": "ab", "k": "size"}, "undefined"},
		{"[{{ s.x }}]", map[string]any{"s": "ab"}, "undefined"},
		{"[{{ h['k'] }}]", map[string]any{"h": map[string]any{"k": 1}}, "[1]"},
		{"[{{ c[i] }}]", map[string]any{"c": []any{1, 2}, "i": "1"}, "undefined"},
		{"[{{ c['size'] }}]", map[string]any{"c": []any{1}}, "undefined"},
		{"[{{ c['size'] }}]", map[string]any{"c": map[string]any{}}, "undefined"},
		{"[{{ c['size'] }}]", map[string]any{"c": map[string]any{"size": "s"}}, "[s]"},
		{"[{{ c['size'] }}]", map[string]any{"c": "abc"}, "undefined"},
		{"[{{ c[0] }}]", map[string]any{"c": "abc"}, "undefined"},
		{"[{{ c[0] }}]", map[string]any{"c": int64(5)}, "undefined"},
		{"[{{ c[0] }}]", map[string]any{"c": true}, "undefined"},
		{"[{{ c.x }}]", map[string]any{"c": 5}, "undefined"},
		{"[{{ c.first }}]", map[string]any{"c": int64(5)}, "undefined"},
		{"[{{ c.size }}]", map[string]any{"c": true}, "undefined"},
		{"{% if c.x == nil %}y{% endif %}", map[string]any{"c": 1.5}, "y"},
		{"[{{ c[i] }}]", map[string]any{"c": []any{1}, "i": json.Number("1.5")}, "undefined"},
		{"[{{ c[i] }}]", map[string]any{"c": map[string]any{"1.5": 1}, "i": json.Number("1.5")}, "undefined"},
		{"[{{ c[i] }}]", map[string]any{"c": map[string]any{"true": 1}, "i": true}, "undefined"},
		{"[{{ c[i] }}]", map[string]any{"c": []any{1}, "i": nil}, "undefined"},
		{"[{{ c[i] }}]", map[string]any{"c": map[string]any{"": 1}, "i": []any{}}, "undefined"},
		{"[{{ c[(1..2)] }}]", map[string]any{"c": []any{1, 2, 3}}, "undefined"},
		{"{% for x in c %}{{ c[forloop.index0] }}{% endfor %}", map[string]any{"c": []any{1, 2}}, "12"},
		{"{% if c[m] %}y{% else %}n{% endif %}", map[string]any{"c": []any{1}}, "n"},
		{"{% if c[m.x] == nil %}y{% endif %}", map[string]any{"c": map[string]any{}}, "y"},
		{"[{{ c[m] }}]", map[string]any{"c": []any{1}}, "undefined"},
	} {
		out, err := e.Render(c.tpl, c.vars, true)
		if errors.Is(err, ErrUndefined) {
			out = "undefined"
		}
		if out != c.want {
			t.Errorf("%s with %v: got %q, %v; want %q", c.tpl, c.vars, out, err, c.want)
		}
	}
	var me *Error
	if _, err := e.Render("{{ h.first }}", map[string]any{"h": map[string]any{"k": 1}}, false); !errors.As(err, &me) || me.Code != "dialect-ruby-hash-first" {
		t.Errorf("Hash first: got %v; want dialect-ruby-hash-first", err)
	}
	if _, err := e.Render("{{ n.size }}", map[string]any{"n": 5}, false); !errors.As(err, &me) || me.Code != "dialect-ruby-number-size" {
		t.Errorf("Integer size: got %v; want dialect-ruby-number-size", err)
	}
	if _, err := e.Render("{{ s.size }}", map[string]any{"s": "a\xff"}, false); !errors.As(err, &me) || me.Code != "invalid-utf8" {
		t.Errorf("invalid UTF-8: got %v; want invalid-utf8", err)
	}
	if out, err := e.Render("[{{ c.p.x }}]", map[string]any{"c": map[string]any{"p": nil}}, false); err != nil || out != "[]" {
		t.Errorf("lax: got %q, %v; want []", out, err)
	}
	if out, err := (Engine{}).Render("[{{ c.p.x }}]", map[string]any{"c": map[string]any{"p": nil}}, true); err != nil || out != "[]" {
		t.Errorf("liquidjs: got %q, %v; want [], a property of null being null", out, err)
	}
	if out, err := e.Render("[{{ c[m] }}]", map[string]any{"c": []any{1}}, false); err != nil || out != "[]" {
		t.Errorf("lax undefined index: got %q, %v; want []", out, err)
	}
	if out, err := e.Render("[{{ m[0] }}]", nil, false); err != nil || out != "[]" {
		t.Errorf("lax index of undefined: got %q, %v; want []", out, err)
	}
	if _, err := e.Render("[{{ c[i] }}]", map[string]any{"c": []any{1}, "i": struct{}{}}, false); !errors.Is(err, ErrUnsupported) {
		t.Errorf("index of an unknown type: got %v; want ErrUnsupported", err)
	}
	if _, err := e.Render("[{{ c[0] }}]", map[string]any{"c": json.Number("1.5")}, false); !errors.As(err, &me) || me.Code != "dialect-ruby-float-index" {
		t.Errorf("index of a Float: got %v; want dialect-ruby-float-index", err)
	}
	e.Dialect.NumberText = true
	if _, err := e.Render("[{{ c[1.0] }}]", map[string]any{"c": map[string]any{}}, true); !errors.Is(err, ErrUndefined) {
		t.Errorf("Float index: got %v; want ErrUndefined", err)
	}
	if _, err := e.Render("[{{ c[1.0] }}]", map[string]any{"c": []any{1, 2}}, true); !errors.Is(err, ErrUndefined) {
		t.Errorf("Float index of an Array: got %v; want ErrUndefined", err)
	}
}

// when matches with the dialect's ==, stopping at each when's first match.
func TestDialectCase(t *testing.T) {
	var calls []call
	e := Engine{Dialect: toyDialect(&calls), Filters: map[string]FilterFunc{
		"dec": func(f Filter) (any, error) { return &toyDecimal{fmt.Sprint(f.Input)}, nil },
	}}
	vars := map[string]any{"n": 2.5}
	for _, c := range []struct {
		tpl, want string
		hooked    bool
	}{
		{"{% case n %}{% when '2.5' %}Y{% else %}N{% endcase %}", "Y", true}, // liquidjs: N
		{"{% assign d = 1 | dec %}{% case d %}{% when 1 %}one{% when d %}self{% endcase %}", "self", false},
		{"{% if false %}{% case n %}{% when 1 %}{% endcase %}{% endif %}", "", false},
	} {
		calls = nil
		if out, err := e.Render(c.tpl, vars, false); err != nil || out != c.want || (calls != nil) != c.hooked {
			t.Errorf("%s: got %q, %v, hook calls %#v; want %q", c.tpl, out, err, calls, c.want)
		}
	}

	calls = nil
	if _, err := e.Render("{% case missing %}{% when nil, 3 %}{% when 3 or 'x' %}{% endcase %}", vars, false); err != nil {
		t.Fatal(err)
	}
	want := []call{{"==", nil, nil}, {"==", nil, int64(3)}, {"==", nil, "x"}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("hook arguments: got %#v; want %#v", calls, want)
	}

	bad := Engine{Dialect: &Dialect{Compare: func(string, any, any) (bool, error) { return false, errToy }}}
	var me *Error
	if _, err := bad.Render("{% case 1 %}{% when 1 %}{% endcase %}", nil, false); !errors.Is(err, errToy) || !errors.As(err, &me) || me.Code != "hook:dialect:compare" {
		t.Errorf("hook error: got %v; want hook:dialect:compare wrapping it", err)
	}
}

// Contains takes over contains, including the cases liquidjs bails on.
func TestDialectContains(t *testing.T) {
	var calls []call
	e := Engine{Dialect: &Dialect{Contains: func(a, b any) (bool, error) {
		calls = append(calls, call{"contains", a, b})
		xs, ok := a.([]any)
		if !ok {
			return false, errToy
		}
		for _, x := range xs {
			if fmt.Sprint(x) == fmt.Sprint(b) {
				return true, nil
			}
		}
		return false, nil
	}}}
	vars := map[string]any{"xs": []any{1, nil}, "s": "abc"}
	out, err := e.Render("{% if xs contains '1' %}a{% endif %}{% if xs contains nil %}b{% endif %}{% if xs contains 3 %}c{% endif %}{% if false %}{% if xs contains 1 %}{% endif %}{% endif %}", vars, false)
	if err != nil || out != "ab" {
		t.Errorf("got %q, %v; want ab", out, err) // liquidjs: b, after bailing on contains nil
	}
	want := []call{{"contains", vars["xs"], "1"}, {"contains", vars["xs"], nil}, {"contains", vars["xs"], int64(3)}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("hook arguments: got %#v; want %#v", calls, want)
	}

	var me *Error
	if _, err := e.Render("{% if s contains 'a' %}{% endif %}", vars, false); !errors.Is(err, errToy) || !errors.As(err, &me) || me.Code != "hook:dialect:contains" {
		t.Errorf("hook error: got %v; want hook:dialect:contains wrapping it", err)
	}
	if _, err := e.Render("{% if xs contains blank %}{% endif %}", vars, false); !errors.As(err, &me) || me.Code != "keyword-outside-equality" {
		t.Errorf("keyword: got %v; want keyword-outside-equality", err)
	}
}

// Outputs are render's legacy (Ruby Liquid 2.5 emulation) engine's.
func TestDialectRubyLax(t *testing.T) {
	e := Engine{Dialect: &Dialect{RubyLax: true}}
	data := map[string]any{"a": 1, "b": 0, "f": false, "t": true, "n": nil, "s": "x"}
	cond := func(c string) string { return "{% if " + c + " %}Y{% else %}N{% endif %}" }
	for tpl, want := range map[string]string{
		cond("a == 1 t"):          "Y",
		cond("a == 2 t"):          "N",
		cond("a == 1, t"):         "Y",
		cond("a == 1 T or f"):     "Y",
		cond("f | default: true"): "N",
		cond("b | plus: 1 > 5"):   "Y",
		cond("a =="):              "N",
		cond("n =="):              "Y",
		cond("n == and t"):        "Y",
		cond("a 1"):               "Y",
		cond("t and f or t"):      "Y",
		cond("f or t and f"):      "N",
		cond("a <> 1"):            "N",
		cond("a == 1 and_x"):      "Y",
		"{% unless a == 1 t %}Y{% endunless %}{% if f %}{% elsif t 1 %}E{% endif %}": "E",
		"{{ a b }}|{{ a, b }}|{{ a == 1 }}|{{ s junk | upcase }}":                    "1|1|1|X",
		"{% assign x = a b %}{{ x }}{% assign y = s | upcase %}{{ y }}":              "1X",
	} {
		if out, err := e.Render(tpl, data, false); err != nil || out != want {
			t.Errorf("%s: got %q, %v; want %q", tpl, out, err, want)
		}
	}
	for tpl, code := range map[string]string{
		cond("a b"):                           "dialect-ruby-operator",
		cond("a = 1"):                         "dialect-ruby-operator",
		cond("not a"):                         "reserved-word-variable",
		cond("a==1"):                          "dialect-ruby-fragment",
		cond(`a == "x"y`):                     "dialect-ruby-fragment",
		cond(`a == 1 "x"`):                    "dialect-ruby-skipped-quote",
		cond("and a"):                         "dialect-ruby-and-or",
		cond("a and"):                         "dialect-ruby-and-or",
		cond("a or, b"):                       "dialect-ruby-and-or",
		`{{ s "q" | upcase }}`:                "dialect-ruby-skipped-quote",
		"{% if false %}{{ a==1 }}{% endif %}": "dialect-ruby-fragment",
	} {
		_, err := e.Render(tpl, data, false)
		var me *Error
		if !errors.As(err, &me) || me.Code != code {
			t.Errorf("%s: got %v; want %s", tpl, err, code)
		}
		if err := e.Check(tpl); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: Check got %v; want ErrUnsupported", tpl, err)
		}
	}
}

// Outputs are render's legacy (Ruby Liquid 2.5 emulation) engine's.
func TestDialectRawStrings(t *testing.T) {
	e := Engine{Dialect: &Dialect{RawStrings: true, RubyLax: true}}
	data := map[string]any{"s": `a\b`}
	for tpl, want := range map[string]string{
		`{{ "a\nb" }}`:                                          `a\nb`,
		`{{ "c:\temp" | append: "\x" }}`:                        `c:\temp\x`,
		`{% assign x = "\u00e9" %}{{ x }}`:                      `\u00e9`,
		`{{ "\101" }}|{{ "\\" }}|{{ "\'" }}`:                    `\101|\\|\'`,
		`{{ "a\" | append: "b" }}|{{ "a\" }}{{ "b" }}`:          `a\b|a\b`,
		`{% if s == "a\b" %}Y{% else %}N{% endif %}`:            "Y",
		`{% case s %}{% when "a\b" %}W{% else %}E{% endcase %}`: "W",
		`{{ s | replace: "\", "/" }}`:                           "a/b",
	} {
		if out, err := e.Render(tpl, data, false); err != nil || out != want {
			t.Errorf("%s: got %q, %v; want %q", tpl, out, err, want)
		}
	}
}

// Outputs are render's legacy (Ruby Liquid 2.5 emulation) engine's.
func TestDialectRubyForElse(t *testing.T) {
	e := Engine{Dialect: &Dialect{RubyForElse: true}}
	data := map[string]any{"xs": []any{1, 2, 3}, "em": []any{}, "n": nil, "i": 3, "z": 0}
	loop := func(head string) string { return "{% for x in " + head + " %}{{ x }}{% else %}E{% endfor %}" }
	for tpl, want := range map[string]string{
		loop("xs offset: 3"):          "E",
		loop("xs limit: 0"):           "E",
		loop("xs reversed offset: 3"): "E",
		loop("xs offset: 1 limit: 1"): "2",
		loop("(1..3) offset: 3"):      "E",
		loop("(i..z) limit: 1"):       "E",
		loop("em limit: 2"):           "E",
		loop("n offset: 1"):           "E",
		loop("missing"):               "E",
		// in a dead branch
		"{% if false %}" + loop("xs limit: 0") + "{% endif %}": "",
		// nested
		"{% for x in xs limit: 1 %}{% for y in em %}{% else %}I{% endfor %}{% endfor %}{% for x in xs offset: 9 %}{% else %}E{% endfor %}": "IE",
	} {
		if out, err := e.Render(tpl, data, false); err != nil || out != want {
			t.Errorf("%s: got %q, %v; want %q", tpl, out, err, want)
		}
	}
}
