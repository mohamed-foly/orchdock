// Package web embeds the user interface (plain HTML/CSS/JS, no build step).
package web

import "embed"

// FS holds the UI files.
//
//go:embed index.html app.js model.js styles.css icons/*.svg icons/lucide/*.svg
var FS embed.FS
