package ui

import tea "github.com/charmbracelet/bubbletea"

// inputOption is nothing on Windows: Bubble Tea reads the console's input
// records there, mouse events included, so no escape sequence gets split.
func inputOption() tea.ProgramOption { return func(*tea.Program) {} }
