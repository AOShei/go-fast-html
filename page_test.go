package gofasthtml

import (
	"strings"
	"testing"
)

// page is an ordinary article page with the usual furniture.
const page = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Quarterly report &mdash; Example Corp</title>
  <meta name="author" content="Jane Doe">
  <link rel="stylesheet" href="/site.css">
  <script>window.dataLayer = [];</script>
</head>
<body>
  <nav><ul><li><a href="/">Home</a></li><li><a href="/news">News</a></li></ul></nav>
  <article id="main">
    <h1>Quarterly report</h1>
    <p class="lead">Revenue grew in <em>every</em> region.</p>
    <h2 id="regions">By region</h2>
    <table>
      <thead><tr><th>Region<th>Revenue</thead>
      <tbody><tr><td>EMEA<td>1.2M<tr><td>APAC<td>0.9M</tbody>
    </table>
    <figure><img src="chart.png" alt="Revenue chart" width="600" height="400">
      <figcaption>Figure 1: revenue by region</figcaption></figure>
    <p>Notes:
    <ol><li>Unaudited.<li>In USD.</ol>
    <pre><code class="language-sql">SELECT region, SUM(revenue)
FROM sales GROUP BY region;</code></pre>
    <blockquote><p>Best quarter yet.</p></blockquote>
  </article>
  <footer><p>&copy; 2026 Example Corp</p></footer>
</body>
</html>`

func TestPage(t *testing.T) {
	doc := extract(t, page, nil)
	want := []string{
		`list_item1[` + "•" + `]"Home"`,
		`list_item1[` + "•" + `]"News"`,
		`heading1"Quarterly report"`,
		`paragraph"Revenue grew in every region."`,
		`heading2"By region"`,
		`table[["Region" "Revenue"] ["EMEA" "1.2M"] ["APAC" "0.9M"]]`,
		`image"Revenue chart"`,
		`caption"Figure 1: revenue by region"`,
		`paragraph"Notes:"`,
		`list_item1[1.]"Unaudited."`,
		`list_item1[2.]"In USD."`,
		`code"SELECT region, SUM(revenue)\nFROM sales GROUP BY region;"`,
		`quote1"Best quarter yet."`,
		`paragraph"` + "©" + ` 2026 Example Corp"`,
	}
	got := show(doc)
	if !strings.Contains(got, strings.Join(want, " ")) {
		t.Errorf("got  %s\nwant %s", got, strings.Join(want, " "))
	}
	if doc.Metadata.Title != "Quarterly report — Example Corp" || doc.Metadata.Language != "en" {
		t.Errorf("metadata %+v", doc.Metadata)
	}
	for _, b := range doc.Parts[0].Blocks {
		if b.Kind.String() == "table" && b.Table.HeaderRows != 1 {
			t.Errorf("header rows %d", b.Table.HeaderRows)
		}
		if b.Kind.String() == "code" && b.Attrs["language"] == "sql" && b.Hidden != 0 {
			t.Error("pre flagged hidden")
		}
	}
}
