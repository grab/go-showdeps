package deps

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// ModalWithInput is a tview.Modal with an additional Search input field.
type ModalWithInput struct {
	*tview.Box
	frame      *tview.Frame
	form       *tview.Form
	inputField *tview.InputField
}

func NewModalWithInput() *ModalWithInput {
	m := &ModalWithInput{
		Box: tview.NewBox(),
	}
	m.form = tview.NewForm()
	m.form.SetBackgroundColor(tview.Styles.ContrastBackgroundColor).SetBorderPadding(0, 0, 0, 0)
	m.form.AddInputField("Search", "", 0, nil, nil)
	m.inputField = m.form.GetFormItem(0).(*tview.InputField)
	m.frame = tview.NewFrame(m.form).SetBorders(0, 0, 0, 0, 0, 0)
	m.frame.SetBorder(true).
		SetBackgroundColor(tview.Styles.ContrastBackgroundColor).
		SetBorderPadding(0, 0, 0, 0)
	return m
}

func (m *ModalWithInput) GetInput() string {
	return m.inputField.GetText()
}

func (m *ModalWithInput) ClearInput() {
	m.inputField.SetText("")
}

func (m *ModalWithInput) Focus(delegate func(p tview.Primitive)) {
	delegate(m.form)
}

func (m *ModalWithInput) HasFocus() bool {
	return m.form.HasFocus()
}

func (m *ModalWithInput) Draw(screen tcell.Screen) {
	screenWidth, screenHeight := screen.Size()
	width := screenWidth / 3

	m.frame.Clear()

	height := 4
	x := (screenWidth - width) / 2
	y := (screenHeight - height) / 2
	m.SetRect(x, y, width, height)

	m.frame.SetRect(x, y, width, height)
	m.frame.Draw(screen)
}

func (m *ModalWithInput) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return m.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		if m.frame.HasFocus() {
			if handler := m.frame.InputHandler(); handler != nil {
				handler(event, setFocus)
				return
			}
		}
	})
}
