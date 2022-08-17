// Package deps contains the logic for the go-showdeps command.
package deps

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/tj/go-spin"
	"github.com/grab/go-showdeps/config"
	"golang.org/x/tools/go/packages"
)

type depType interface {
	Color() string
	Priority() int
	Label() string
}

type dtImpl struct {
	color    string
	priority int
	label    string
}

func (dt *dtImpl) Color() string {
	return dt.color
}

func (dt *dtImpl) Priority() int {
	return dt.priority
}

func (dt *dtImpl) Label() string {
	return dt.label
}

func newDepType(color, label string, priority int) *dtImpl {
	return &dtImpl{
		color:    color,
		label:    label,
		priority: priority,
	}
}

var (
	depTypeStdlib depType = newDepType("#aaaaaa", "Standard library", 0)
	depTypeVendor depType = newDepType("#9b59b6", "Vendor package", 1)
)

type depEntry struct {
	pkg     string
	depType depType
	hidden  bool
}

type depsGen struct {
	serviceRoot string
	readyCh     chan struct{}
	loadStage   chan string
	config      config.Config
	rules       map[*regexp.Regexp]depType
}

func (d *depEntry) Str(stripPath bool, pathPrefix string) string {
	if stripPath {
		return strings.Replace(d.pkg, pathPrefix, "", 1)
	}
	return d.pkg
}

// ShowDeps is the main function.
func ShowDeps(serviceRoot string, cfg config.Config) error {
	d := depsGen{
		serviceRoot: serviceRoot,
		readyCh:     make(chan struct{}),
		loadStage:   make(chan string),
		config:      cfg,
		rules:       make(map[*regexp.Regexp]depType),
	}

	for _, rule := range d.config.Rules {
		rx, err := regexp.Compile(rule.Regex)
		if err != nil {
			return err
		}
		d.rules[rx] = newDepType(rule.Color, rule.Label, rule.Priority)
	}

	d.startSpinner()
	d.loadStage <- "services"

	return d.showDeps()
}

func (d *depsGen) startSpinner() {
	go func() {
		loadStage := "init"
		s := spin.New()
		for {
			select {
			case newLoadStage := <-d.loadStage:
				loadStage = newLoadStage
			case <-d.readyCh:
				return
			case <-time.After(100 * time.Millisecond):
				fmt.Printf("\r loading %s\033[38;2;255;255;0m %s                \r\033[0m", loadStage, s.Next())
			}
		}
	}()
}

type depMap map[string]map[string]struct{}

func (d *depsGen) getDependencies(scanner *bufio.Scanner) (depMap, depMap, map[string]bool) {
	deps := map[string]map[string]struct{}{}
	children := map[string]map[string]struct{}{}
	isInModule := map[string]bool{}

	// Build up the dep graph from the go list output
	for scanner.Scan() {
		text := scanner.Text()
		pkgs := strings.Split(text, " ")
		if _, ok := deps[pkgs[0]]; !ok {
			deps[pkgs[0]] = map[string]struct{}{}
		}
		for _, p := range pkgs[2:] {
			deps[pkgs[0]][p] = struct{}{}
		}

		// Get root module
		rootMod := pkgs[1]
		if rootMod != "_" {
			if _, ok := children[rootMod]; !ok {
				children[rootMod] = map[string]struct{}{}
			}
			if pkgs[0] != rootMod {
				children[rootMod][pkgs[0]] = struct{}{}
				isInModule[pkgs[0]] = true
			}
		}
	}
	return deps, children, isInModule
}

func (d *depsGen) startGoListPipe() (*exec.Cmd, io.Reader, error) {
	// Get deps from go list, because for forward deps it's fast enough
	goListCmd := exec.Command("go", "list", "-deps", "-e", "-f", `{{.ImportPath}} {{if .Module }}{{.Module.Path}}{{else}}_{{end}} {{join .Imports " "}}`, "./...")
	pipe, perr := goListCmd.StdoutPipe()
	if perr != nil {
		return nil, nil, perr
	}
	if err := goListCmd.Start(); err != nil {
		return nil, nil, err
	}
	return goListCmd, pipe, nil
}

func (d *depsGen) findOwnModule() (string, error) {
	selfListCmd := exec.Command("go", "list", "-f", `{{.ImportPath}}{{if .Module }} {{.Module.Path}}{{end}}`)
	modOut, err := selfListCmd.Output()
	if err != nil {
		return "", fmt.Errorf("error from go list for own module: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(string(modOut)), " ")
	if len(parts) == 2 && parts[1] != "" {
		return parts[1], nil
	}
	return "", nil
}

func (d *depsGen) flattenDeps(deps depMap, isInModule map[string]bool) map[string]struct{} {
	// Convert to a flat list of forward dependencies
	flat := map[string]struct{}{}
	for _, v := range deps {
		for pkg := range v {
			if !isInModule[pkg] {
				flat[pkg] = struct{}{}
			}
		}
	}
	return flat
}

func (d *depsGen) showDeps() error {
	d.loadStage <- "go list"
	goListCmd, pipe, err := d.startGoListPipe()
	if err != nil {
		return err
	}

	deps, children, isInModule := d.getDependencies(bufio.NewScanner(pipe))

	flat := d.flattenDeps(deps, isInModule)

	std := getStandardPkgs()

	finalList := []depEntry{}

	for mod := range children {
		flat[mod] = struct{}{}
	}

	// try to find own module
	d.loadStage <- "own module "
	selfPkg, err := d.findOwnModule()
	if err != nil {
		return err
	}

	d.loadStage <- "classifications"

	for pkg := range flat {
		if pkg == "" || pkg == selfPkg {
			continue
		}
		dt := d.classify(pkg, std, children)
		finalList = append(finalList, depEntry{
			pkg:     pkg,
			depType: dt,
		})
	}

	sort.Slice(finalList, func(i, j int) bool {
		if finalList[i].depType == finalList[j].depType {
			return finalList[i].pkg < finalList[j].pkg
		}
		return finalList[i].depType.Priority() > finalList[j].depType.Priority()
	})

	invertedGraph := d.invertGraph(deps)

	d.loadStage <- "the UI"
	d.drawDeps(selfPkg, finalList, children, deps, invertedGraph)
	if err := goListCmd.Wait(); err != nil {
		return fmt.Errorf("error from go list: %w", err)
	}
	return nil
}

func (d *depsGen) invertGraph(src depMap) depMap {
	invertedGraph := depMap{}
	for from, v := range src {
		for to := range v {
			if _, ok := invertedGraph[to]; !ok {
				invertedGraph[to] = map[string]struct{}{}
			}
			invertedGraph[to][from] = struct{}{}
		}
	}
	return invertedGraph
}

func (d *depsGen) classify(pkg string, std map[string]struct{}, children map[string]map[string]struct{}) depType {
	// Check for custom rules first
	for rx, dt := range d.rules {
		if rx.MatchString(pkg) {
			return dt
		}
	}

	if isStandardPackage(std, pkg) {
		return depTypeStdlib
	}

	return depTypeVendor
}

func (d *depsGen) findPath(fwd, rev map[string]map[string]struct{}, key string) []string {
	ret := []string{key}
	for {
		next := rev[key]
		if next == nil {
			break
		}
		for k := range next {
			key = k
			break
		}
		ret = append(ret, key)
	}
	return ret
}

func (d *depsGen) makeListChangedFunc(packagesList *tview.List, info *tview.TextView, depTypes map[string]depType, children depMap) func(idx int, s, pkg string, _ rune) {
	return func(idx int, s, pkg string, _ rune) {
		packagesList.Clear()
		info.SetText(fmt.Sprintf("[%s]%s", depTypes[pkg].Color(), depTypes[pkg].Label()))
		// Only add the package itself if it is not a parent
		if len(children[pkg]) == 0 {
			packagesList.AddItem(s, pkg, 0, nil)
			return
		}
		for c := range children[pkg] {
			cstr := c
			if d.config.StripPath {
				cstr = strings.Replace(cstr, d.config.PathPrefix, "", 1)
			}
			packagesList.AddItem(fmt.Sprintf("[%s]%s", depTypes[pkg].Color(), cstr), c, 0, nil)
		}
	}
}

func (d *depsGen) makePackagesListChangedFunc(importPathTable *tview.Table, depGraph, invertedGraph depMap) func(idx int, _, pkg string, _ rune) {
	return func(idx int, _, pkg string, _ rune) {
		importPathTable.Clear()
		path := d.findPath(depGraph, invertedGraph, pkg)
		x := 0
		for i := len(path) - 1; i >= 0; i-- {
			pkg := path[i]
			if d.config.StripPath {
				pkg = strings.Replace(pkg, d.config.PathPrefix, "", 1)
			}
			importPathTable.SetCell(x, 0, tview.NewTableCell(pkg).SetExpansion(1).SetAlign(tview.AlignCenter))
			x++
			if i > 0 {
				importPathTable.SetCell(x, 0, tview.NewTableCell("↓").SetExpansion(1).SetAlign(tview.AlignCenter).SetTextColor(tcell.NewRGBColor(0, 177, 79)))
			}
			x++
		}
		if cell := importPathTable.GetCell(0, 0); cell != nil {
			cell.SetTextColor(tcell.NewRGBColor(52, 152, 219))
		}
		if len(path) > 1 {
			if cell := importPathTable.GetCell((len(path)*2)-2, 0); cell != nil {
				cell.SetTextColor(tcell.NewRGBColor(211, 84, 0))
			}
		}
	}
}

func (d *depsGen) drawDeps(selfPkg string, deps []depEntry, children, depGraph, invertedGraph depMap) {
	depTypes := map[string]depType{}
	app := tview.NewApplication()
	importsGrid := tview.NewGrid()
	importsGrid.SetBorder(true).SetTitle("Imports")
	packagesGrid := tview.NewGrid()
	packagesGrid.SetBorder(true).SetTitle("Packages")
	importPathGrid := tview.NewGrid()
	importPathGrid.SetBorder(true).SetTitle("Import Path")
	header := tview.NewTextView().
		SetTextColor(tcell.ColorWhite).
		SetDynamicColors(true).
		SetText(fmt.Sprintf("  [#ffffff::b]go-showdeps[::-] - [#ffff00]%s", selfPkg))
	header.SetBackgroundColor(tcell.NewHexColor(0x00B14F))
	footer := tview.NewTextView().
		SetDynamicColors(true).
		SetTextColor(tcell.NewHexColor(0x005339)).
		SetText(fmt.Sprintf("  [#005339::b]%d deps found. [#3c73a8](h)elp (f)ind (r)eset (q)uit (0-9):filter by type", len(deps)))
	info := tview.NewTextView().SetDynamicColors(true)
	info.SetBorder(true).SetTitle("Info").SetBorderPadding(0, 0, 1, 1)
	footer.SetBackgroundColor(tcell.NewHexColor(0xD9FCDE))

	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 1, 0, false).
		AddItem(tview.NewFlex().AddItem(importsGrid, 0, 1, true).
			AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
				AddItem(info, 4, 0, false).
				AddItem(packagesGrid, 0, 1, false).
				AddItem(importPathGrid, 0, 2, false), 0, 1, false), 0, 1, true).
		AddItem(footer, 1, 0, false)

	list := tview.NewList().ShowSecondaryText(false)
	packagesList := tview.NewList().ShowSecondaryText(false)
	importPathTable := tview.NewTable().SetBorders(false)

	list.SetChangedFunc(d.makeListChangedFunc(packagesList, info, depTypes, children))

	packagesList.SetChangedFunc(d.makePackagesListChangedFunc(importPathTable, depGraph, invertedGraph))

	for _, dep := range deps {
		depTypes[dep.pkg] = dep.depType
	}
	d.addDepsToList(deps, list)
	importPathGrid.AddItem(importPathTable, 0, 0, 1, 1, 0, 0, true)
	packagesGrid.AddItem(packagesList, 0, 0, 1, 1, 0, 0, true)
	importsGrid.AddItem(list, 0, 0, 1, 1, 0, 0, true)
	pages := tview.NewPages()
	modal := NewModalWithInput()
	helpModal := tview.NewModal().
		SetText("Browse dependencies using the arrow keys, then tab to the Packages window to see the import path for a specific package.\n\nUse 'f' to search for a specific package or 1-7 to quickly filter packages by type. Press 'r' to reset the filter.\n\nPackages are coloured and sorted according to type of dependency shown in the Info window.").
		AddButtons([]string{"Ok"}).SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		pages.HidePage("helpModal")
	})
	pages.AddPage("flex", flex, true, true).
		AddPage("modal", modal, false, false).
		AddPage("helpModal", helpModal, false, false)
	pages.HidePage("modal")
	d.setupInputCapture(app, modal, helpModal, deps, list, pages, packagesGrid, importsGrid, importPathGrid)

	close(d.readyCh)
	if err := app.SetRoot(pages, true).SetFocus(pages).Run(); err != nil {
		panic(err)
	}
}

func (d *depsGen) setupInputCapture(app *tview.Application, modal *ModalWithInput, helpModal *tview.Modal, deps []depEntry, list *tview.List, pages *tview.Pages, packagesGrid, importsGrid, importPathGrid *tview.Grid) {
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyRune:
			if modal.HasFocus() || helpModal.HasFocus() {
				return event
			}
			switch event.Rune() {
			case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
				i := int(event.Rune() - '0')
				d.filterListByType(deps, list, i)
			case 'r':
				d.filterList(deps, list, "")
			case 'q':
				app.Stop()
			case 'f':
				modal.ClearInput()
				pages.ShowPage("modal")
				return nil
			case 'h':
				pages.ShowPage("helpModal")
				return nil
			}
		case tcell.KeyTab:
			if packagesGrid.HasFocus() {
				app.SetFocus(importPathGrid)
			} else if importsGrid.HasFocus() {
				app.SetFocus(packagesGrid)
			} else if importPathGrid.HasFocus() {
				app.SetFocus(importsGrid)
			}
			return nil
		case tcell.KeyRight:
			if packagesGrid.HasFocus() {
				app.SetFocus(importPathGrid)
			} else if importsGrid.HasFocus() {
				app.SetFocus(packagesGrid)
			} else if importPathGrid.HasFocus() {
				app.SetFocus(importsGrid)
			}
			return nil
		case tcell.KeyLeft:
			if packagesGrid.HasFocus() {
				app.SetFocus(importsGrid)
			} else if importsGrid.HasFocus() {
				app.SetFocus(packagesGrid)
			} else if importPathGrid.HasFocus() {
				app.SetFocus(importsGrid)
			}
			return nil
		case tcell.KeyEnter:
			if modal.HasFocus() {
				pages.HidePage("modal")
				s := modal.GetInput()
				d.filterList(deps, list, s)
				app.SetFocus(list)
				return nil
			}
		}

		return event
	})
}

func (d *depsGen) filterList(deps []depEntry, list *tview.List, s string) {
	for i := range deps {
		deps[i].hidden = true
		if strings.Contains(deps[i].pkg, s) {
			deps[i].hidden = false
		}
	}
	list.Clear()
	d.addDepsToList(deps, list)
}

func (d *depsGen) filterListByType(deps []depEntry, list *tview.List, priority int) {
	for i := range deps {
		deps[i].hidden = true
		if deps[i].depType.Priority() == priority {
			deps[i].hidden = false
		}
	}
	list.Clear()
	d.addDepsToList(deps, list)
}

func (d *depsGen) addDepsToList(deps []depEntry, list *tview.List) {
	for _, dep := range deps {
		if dep.hidden {
			continue
		}
		list.AddItem(fmt.Sprintf("[%s]%s", dep.depType.Color(), dep.Str(d.config.StripPath, d.config.PathPrefix)), dep.pkg, 0, nil)
	}
}

func getStandardPkgs() map[string]struct{} {
	standardPackages := map[string]struct{}{}
	pkgs, err := packages.Load(nil, "std")
	if err != nil {
		panic(err)
	}

	for _, p := range pkgs {
		standardPackages[p.PkgPath] = struct{}{}
	}
	return standardPackages
}

func isStandardPackage(standardPackages map[string]struct{}, pkg string) bool {
	_, ok := standardPackages[pkg]
	return ok
}
