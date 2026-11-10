package docparse

import (
	"bytes"
	"path"
	"strings"

	"golang.org/x/net/html"
)

type HTMLMetadata struct {
	Title     string
	PlainText string
	Assets    []string
}

// ParseHTML 提取 HTML 文档的标题、可检索文本和仓库内资源。
func ParseHTML(source, docPath string) HTMLMetadata {
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return HTMLMetadata{Title: strings.TrimSuffix(path.Base(docPath), path.Ext(docPath))}
	}
	title := strings.TrimSpace(textContent(findElement(document, "title")))
	heading := strings.TrimSpace(textContent(findElement(document, "h1")))
	body := findElement(document, "body")
	if body == nil {
		body = document
	}
	return HTMLMetadata{
		Title:     firstNonEmpty(title, heading, strings.TrimSuffix(path.Base(docPath), path.Ext(docPath))),
		PlainText: visibleText(body),
		Assets:    htmlAssets(document, docPath),
	}
}

// PrepareHTML 清理可覆盖资源基址的标签，并注入受控的 base 地址。
func PrepareHTML(source, baseHref string) (string, error) {
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return "", err
	}
	head := findElement(document, "head")
	if head == nil {
		htmlNode := findElement(document, "html")
		if htmlNode == nil {
			return source, nil
		}
		head = &html.Node{Type: html.ElementNode, Data: "head"}
		htmlNode.InsertBefore(head, htmlNode.FirstChild)
	}
	for child := head.FirstChild; child != nil; {
		next := child.NextSibling
		if child.Type == html.ElementNode && strings.EqualFold(child.Data, "base") {
			head.RemoveChild(child)
		}
		if child.Type == html.ElementNode && strings.EqualFold(child.Data, "meta") &&
			strings.EqualFold(attribute(child, "http-equiv"), "refresh") {
			head.RemoveChild(child)
		}
		child = next
	}
	base := &html.Node{
		Type: html.ElementNode,
		Data: "base",
		Attr: []html.Attribute{{Key: "href", Val: baseHref}},
	}
	head.InsertBefore(base, head.FirstChild)

	var output bytes.Buffer
	if err := html.Render(&output, document); err != nil {
		return "", err
	}
	return output.String(), nil
}

func isHTMLPath(value string) bool {
	extension := strings.ToLower(path.Ext(value))
	return extension == ".html" || extension == ".htm"
}

func findElement(root *html.Node, name string) *html.Node {
	if root == nil {
		return nil
	}
	if root.Type == html.ElementNode && strings.EqualFold(root.Data, name) {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := findElement(child, name); found != nil {
			return found
		}
	}
	return nil
}

func textContent(root *html.Node) string {
	if root == nil {
		return ""
	}
	var values []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			values = append(values, node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return normalizeText(strings.Join(values, " "))
}

func visibleText(root *html.Node) string {
	if root == nil {
		return ""
	}
	var values []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch strings.ToLower(node.Data) {
			case "script", "style", "noscript", "template", "svg", "head":
				return
			}
		}
		if node.Type == html.TextNode {
			values = append(values, node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return normalizeText(strings.Join(values, " "))
}

func htmlAssets(root *html.Node, docPath string) []string {
	seen := make(map[string]struct{})
	var assets []string
	add := func(assetPath string) {
		if _, exists := seen[assetPath]; exists {
			return
		}
		seen[assetPath] = struct{}{}
		assets = append(assets, assetPath)
	}
	collect := func(candidate string) {
		assetPath, ok := normalizeAssetPath(docPath, candidate)
		if !ok {
			return
		}
		add(assetPath)
	}
	collectCSS := func(value string) {
		for _, assetPath := range ExtractCSSAssets(value, docPath) {
			add(assetPath)
		}
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			for _, attr := range node.Attr {
				switch strings.ToLower(attr.Key) {
				case "src", "href", "poster", "data":
					collect(attr.Val)
				case "srcset":
					for _, item := range strings.Split(attr.Val, ",") {
						fields := strings.Fields(strings.TrimSpace(item))
						if len(fields) > 0 {
							collect(fields[0])
						}
					}
				case "style":
					collectCSS(attr.Val)
				}
			}
			if strings.EqualFold(node.Data, "style") {
				collectCSS(textContent(node))
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return assets
}

func attribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return attr.Val
		}
	}
	return ""
}

func normalizeText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
