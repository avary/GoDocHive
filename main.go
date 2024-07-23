package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"golang.org/x/net/html"
)

type Document struct {
	Title   string
	Content string
	URL     string
}

var index bleve.Index

func main() {
	var err error
	index, err = bleve.Open("index.bleve")
	if err == bleve.ErrorIndexPathDoesNotExist {
		indexMapping := bleve.NewIndexMapping()
		documentMapping := bleve.NewDocumentMapping()

		textFieldMapping := bleve.NewTextFieldMapping()
		textFieldMapping.Analyzer = standard.Name

		documentMapping.AddFieldMappingsAt("Title", textFieldMapping)
		documentMapping.AddFieldMappingsAt("Content", textFieldMapping)
		documentMapping.AddFieldMappingsAt("URL", textFieldMapping)

		indexMapping.AddDocumentMapping("document", documentMapping)

		index, err = bleve.New("index.bleve", indexMapping)
		if err != nil {
			log.Fatal(err)
		}
		buildIndex()
	} else if err != nil {
		log.Fatal(err)
	}
	defer index.Close()

	http.HandleFunc("/", serveFiles)
	http.HandleFunc("/search", handleSearch)

	fmt.Println("Server running at http://localhost:3030")
	log.Fatal(http.ListenAndServe(":3030", nil))
}

func buildIndex() {
	batch := index.NewBatch()
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".html") {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			title, bodyContent := extractTitleAndContent(string(content))
			if title == "" {
				title = info.Name()
			}

			doc := Document{
				Title:   title,
				Content: bodyContent,
				URL:     path,
			}

			err = batch.Index(path, doc)
			if err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		log.Fatal(err)
	}

	err = index.Batch(batch)
	if err != nil {
		log.Fatal(err)
	}
}

func extractTitleAndContent(content string) (string, string) {
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return "", ""
	}

	var title string
	var bodyContent strings.Builder

	var extract func(*html.Node)
	extract = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "title" && n.FirstChild != nil {
				title = n.FirstChild.Data
			} else if n.Data == "body" {
				extractText(n, &bodyContent)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extract(c)
		}
	}

	extract(doc)
	return title, bodyContent.String()
}

func extractText(n *html.Node, sb *strings.Builder) {
	if n.Type == html.TextNode {
		sb.WriteString(n.Data)
		sb.WriteString(" ")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractText(c, sb)
	}
}

func serveFiles(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, r.URL.Path[1:])
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	var results []Document

	if query != "" {
		searchQuery := bleve.NewMatchQuery(query)
		searchRequest := bleve.NewSearchRequest(searchQuery)
		searchRequest.Fields = []string{"Title", "Content", "URL"}
		searchRequest.Highlight = bleve.NewHighlight()
		searchResult, err := index.Search(searchRequest)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for _, hit := range searchResult.Hits {
			doc := Document{
				Title:   hit.Fields["Title"].(string),
				Content: hit.Fields["Content"].(string),
				URL:     hit.Fields["URL"].(string),
			}
			results = append(results, doc)
		}
	}

	tmpl := template.New("search")

	tmpl.Funcs(template.FuncMap{
		"truncate": func(s string, l int) string {
			if len(s) > l {
				return s[:l] + "..."
			}
			return s
		},
	})

	tmpl, err := tmpl.Parse(`
<!DOCTYPE html>
<html>
<head>
    <title>Go Doc Server :: Search</title>
</head>
<body>
    <div class="row">
        <form action="/search" method="GET">
            <input type="search" id="search_textbox" name="q" value="{{.Query}}">
            <button type="submit">Search</button>
        </form>
    </div>
    <ul>
        {{range .Results}}
        <li>
            <h3><a href="/{{.URL}}">{{.Title}}</a></h3>
            <p>{{.Content | truncate 150}}</p>
        </li>
        {{end}}
    </ul>
    <style>
        .row {
            padding: 1%;
        }
    </style>
</body>
</html>
`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := struct {
		Query   string
		Results []Document
	}{
		Query:   query,
		Results: results,
	}

	err = tmpl.Execute(w, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}