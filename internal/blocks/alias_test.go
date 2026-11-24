package blocks

import (
	"context"
	"reflect"
	"testing"
	"unsafe"

	"github.com/AOShei/go-fast-html/internal/dom"
)

// TestNoAliasOfPage: the tree's strings share the page's memory (the
// lexer copies nothing it need not); no string of the result may, or a
// document kept for long would keep its whole page alive.
func TestNoAliasOfPage(t *testing.T) {
	src := []byte(`<html lang="en-GB"><head><title>T</title><meta name="author" content="A">
<meta name="description" content="D"><style>p{}</style><script src="/s.js">var x</script></head>
<body><h1 id="top">Head</h1><p title="tip" aria-label="lab">Text <a href="/link" name="n">link</a>
<img src="/i.png" alt="alt"> <img src="data:image/png;base64,iVBORw0KGgo=" alt="inline"> <b>bold</b></p><!-- note --><pre class="language-go"><code>code</code></pre>
<ul><li>one<li><input type="checkbox" checked> two</ul><table><tr><th>h<td><a href="/c">c</a></table>
<form><input type="hidden" name="field" value="v"><select name="s"><option>o1<option label="l2"></select>
<textarea name="ta">tt</textarea></form><iframe srcdoc="<p>inner</p>"></iframe></body></html>`)
	tree, err := dom.Parse(context.Background(), src, dom.Options{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Walk(context.Background(), tree.Root, Config{Spans: true, Source: func(i int) int64 { return int64(i) }})
	if err != nil {
		t.Fatal(err)
	}
	lo := uintptr(unsafe.Pointer(&src[0]))
	hi := lo + uintptr(len(src))
	n := 0
	var walk func(v reflect.Value, path string)
	walk = func(v reflect.Value, path string) {
		switch v.Kind() {
		case reflect.String:
			if s := v.String(); s != "" {
				n++
				if p := uintptr(unsafe.Pointer(unsafe.StringData(s))); p >= lo && p < hi {
					t.Errorf("%s = %q shares the page's memory", path, s)
				}
			}
		case reflect.Pointer, reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem(), path)
			}
		case reflect.Struct:
			for i := range v.NumField() {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i), path+"."+v.Type().Field(i).Name)
				}
			}
		case reflect.Slice, reflect.Array:
			for i := range v.Len() {
				walk(v.Index(i), path+"[]")
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				walk(k, path+"{key}")
				walk(v.MapIndex(k), path+"{"+k.String()+"}")
			}
		}
	}
	walk(reflect.ValueOf(res).Elem(), "Result")
	if n < 30 {
		t.Fatalf("only %d strings checked", n)
	}
}
