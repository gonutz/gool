package main

// TODO On close, stop the running program, if any.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/gonutz/w32/v3"
)

func main() {
	if err := run(); err != nil {
		w32.MessageBox(
			0,
			w32.String(err.Error()),
			w32.String("Error"),
			w32.MB_ICONERROR|w32.MB_OK|w32.MB_TOPMOST,
		)
		panic(err)
	}
}

const (
	projectsID = 1 + iota
	startButtonID
	refreshShortcutID
	synchCodeWithRepoID
	startButtonShortcutID
	largerFontShortcutID
	smallerFontShortcutID
	fileExplorerShortcutID
	commandLineShortcutID
	programTimerID
	scrollCheckTimerID
)

const (
	programStartMessage = w32.WM_USER + iota
	programStopMessage
)

var fontSize float64 = 17

const (
	minFontSize = 8
	maxFontSize = 1000
)

func run() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hideConsoleWindow()

	var (
		programMu       sync.Mutex
		programRunning  bool
		stopProgram     = func() {}
		programStdin    io.WriteCloser
		openFilePath    string
		labelFont       w32.HFONT
		codeFont        w32.HFONT
		lastLineCount         = -1
		lastTopCodeLine int32 = -1
	)

	projectsDir := func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.Join(filepath.Dir(exe), "gool_projects"), nil
	}

	fileToOpen := ""
	if root, err := projectsDir(); err != nil {
		return err
	} else if !pathExists(root) {
		// Create the projects folder and a hello world project.
		os.MkdirAll(root, 0666)
		os.MkdirAll(filepath.Join(root, "hello_world"), 0666)
		hello := filepath.Join(root, "hello_world", "main.go")
		os.WriteFile(hello, []byte(helloWorldCode), 0666)
		fileToOpen = hello
	}

	if err := setManifest(); err != nil {
		return err
	}

	if err := w32.InitCommonControlsEx(&w32.INITCOMMONCONTROLSEX{
		ICC: w32.ICC_TREEVIEW_CLASSES |
			w32.ICC_UPDOWN_CLASS |
			w32.ICC_HOTKEY_CLASS |
			w32.ICC_LISTVIEW_CLASSES |
			w32.ICC_PAGESCROLLER_CLASS |
			w32.ICC_PROGRESS_CLASS |
			w32.ICC_TAB_CLASSES,
	}); err != nil {
		return err
	}

	cursor, err := w32.LoadCursor(0, w32.MakeIntResource(w32.IDC_ARROW))
	if err != nil {
		return err
	}

	module, err := w32.GetModuleHandle(nil)
	if err != nil {
		return err
	}

	iconHandle, err := w32.LoadImage(
		module,
		w32.MakeIntResource(7),
		w32.IMAGE_ICON,
		0,
		0,
		w32.LR_DEFAULTSIZE|w32.LR_SHARED,
	)
	if err != nil {
		return err
	}
	icon := w32.HICON(iconHandle)

	background, err := w32.GetSysColorBrush(w32.COLOR_BTNFACE)
	if err != nil {
		return err
	}

	var handleMessage func(window w32.HWND, message uint32, w, l uintptr) uintptr

	class, err := w32.RegisterClassEx(&w32.WNDCLASSEX{
		ClassName:  w32.String("gool_window_class"),
		Style:      w32.CS_HREDRAW | w32.CS_VREDRAW,
		Cursor:     cursor,
		Icon:       icon,
		Background: background,
		WndProc: w32.NewWindowProcedure(
			func(window w32.HWND, message uint32, w, l uintptr) uintptr {
				if handleMessage != nil {
					return handleMessage(window, message, w, l)
				}
				return w32.DefWindowProc(window, message, w, l)
			},
		),
	})
	if err != nil {
		return err
	}
	defer w32.UnregisterClass(w32.StringAtom(class), 0)

	window, err := w32.CreateWindowEx(
		w32.WS_EX_LAYERED|w32.WS_EX_COMPOSITED,
		w32.StringAtom(class),
		w32.String("Gool"),
		w32.WS_OVERLAPPEDWINDOW|w32.WS_VISIBLE,
		w32.CW_USEDEFAULT, w32.CW_USEDEFAULT, w32.CW_USEDEFAULT, w32.CW_USEDEFAULT,
		0, 0, 0,
		nil,
	)
	if err != nil {
		return err
	}

	w32.ShowWindow(window, w32.SW_MAXIMIZE)

	projectsCaption, err := w32.CreateWindowEx(
		0,
		w32.String("STATIC"),
		w32.String("Projekte"),
		w32.WS_VISIBLE|w32.WS_CHILD|w32.ES_CENTER,
		10, 10, 200, 25,
		window,
		0, 0, nil,
	)
	if err != nil {
		return err
	}

	projectTree, err := w32.CreateWindowEx(
		0,
		w32.WC_TREEVIEW,
		nil,
		w32.WS_VISIBLE|w32.WS_CHILD|w32.WS_BORDER|
			w32.TVS_HASLINES|w32.TVS_HASBUTTONS|w32.TVS_LINESATROOT|
			w32.TVS_SHOWSELALWAYS,
		10, 40, 200, 200,
		window,
		projectsID,
		0,
		nil,
	)

	startButton, err := w32.CreateWindowEx(
		0,
		w32.String("BUTTON"),
		w32.String("Start"),
		w32.WS_VISIBLE|w32.WS_CHILD|w32.WS_DISABLED,
		10, 330, 80, 25,
		window,
		startButtonID, 0, nil,
	)
	if err != nil {
		return err
	}

	codeCaption, err := w32.CreateWindowEx(
		0,
		w32.String("STATIC"),
		w32.String("Code"),
		w32.WS_VISIBLE|w32.WS_CHILD,
		220, 10, 200, 25,
		window,
		0, 0, nil,
	)
	if err != nil {
		return err
	}

	codeEdit, err := w32.CreateWindowEx(
		w32.WS_EX_CLIENTEDGE,
		w32.String("EDIT"),
		nil,
		w32.WS_VISIBLE|w32.WS_CHILD|w32.ES_MULTILINE|w32.ES_WANTRETURN|
			w32.WS_HSCROLL|w32.ES_AUTOHSCROLL|w32.WS_VSCROLL|w32.ES_AUTOVSCROLL|
			w32.WS_DISABLED,
		220, 40, 300, 300,
		window,
		0, 0, nil,
	)
	if err != nil {
		return err
	}
	w32.SetFocus(codeEdit)
	w32.Edit_LimitText(codeEdit, 0x7FFFFFFF)

	lineNumbers, err := w32.CreateWindowEx(
		0,
		w32.String("EDIT"),
		w32.String(numberRange(1, 1)),
		w32.WS_CHILD|w32.ES_MULTILINE|w32.WS_DISABLED|
			w32.ES_AUTOHSCROLL|w32.ES_RIGHT,
		210, 40, 10, 300,
		window,
		0, 0, nil,
	)
	if err != nil {
		return err
	}

	consoleOutput, err := w32.CreateWindowEx(
		w32.WS_EX_CLIENTEDGE,
		w32.String("EDIT"),
		w32.String("Programm-Output..."),
		w32.WS_VISIBLE|w32.WS_CHILD|w32.ES_MULTILINE|w32.ES_WANTRETURN|w32.ES_READONLY|
			w32.WS_HSCROLL|w32.ES_AUTOHSCROLL|w32.WS_VSCROLL|w32.ES_AUTOVSCROLL,
		220, 320, 300, 100,
		window,
		0, 0, nil,
	)
	if err != nil {
		return err
	}

	consoleInput, err := w32.CreateWindowEx(
		w32.WS_EX_CLIENTEDGE,
		w32.String("EDIT"),
		w32.String("Programm-Input"),
		w32.WS_VISIBLE|w32.WS_CHILD|w32.WS_DISABLED,
		220, 430, 300, 25,
		window,
		0, 0, nil,
	)
	if err != nil {
		return err
	}
	w32.SendMessage(
		consoleInput,
		w32.EM_SETCUEBANNER,
		w32.TRUE,
		uintptr(unsafe.Pointer(w32.String("Programm-Input..."))),
	)

	// We might need to sync our line numbers with the code when:
	// - the user scrolls the code
	// - the code changes
	// - the font size changes
	// - the window size changes
	// Note that we do not always need to update the line numbers. For example,
	// when the code changes, we only need to update it when the top-most
	// visible line number changes or when the line count changes and we are
	// scrolled down to the bottom of the code.
	// Doing all these checks is too much code, though, so we take some
	// shortcuts.
	updateLineNumbers := func() {
		topLine := w32.Edit_GetFirstVisibleLine(codeEdit)
		r, _ := w32.GetWindowRect(lineNumbers)
		// bottomLine is the maximum visible line at the bottom. The font size
		// in pixels is actually larger than fontSize so this overshoots by a
		// couple of lines. This is fine, a couple extra lines outside the
		// visible area do not hurt. +1 for very large fonts.
		bottomLine := topLine + (r.Bottom-r.Top)/int32(fontSize) + 1
		lineCount := int32(w32.Edit_GetLineCount(codeEdit))
		if lineCount < bottomLine {
			bottomLine = lineCount
		}
		w32.SetWindowText(
			lineNumbers,
			w32.String(numberRange(int(topLine)+1, int(bottomLine))),
		)
	}

	layoutControls := func() {
		r, err := w32.GetClientRect(window)
		if err != nil {
			return
		}
		width, height := int(r.Right-r.Left), int(r.Bottom-r.Top)

		setPos := func(window w32.HWND, x, y, width, height int) {
			w32.SetWindowPos(
				window, 0,
				int32(x), int32(y), int32(width), int32(height),
				w32.SWP_NOOWNERZORDER|w32.SWP_NOZORDER,
			)
		}

		numberW := 50
		if dc, err := w32.GetDC(lineNumbers); err == nil {
			w32.SelectObject(dc, w32.HGDIOBJ(codeFont))
			lineCount := w32.Edit_GetLineCount(codeEdit)
			n := strconv.Itoa(int(lineCount))
			if size, err := w32.GetTextExtentPoint32(dc, w32.String(n)); err == nil {
				numberW = int(size.Cx) * 3 / 2
			}
			w32.ReleaseDC(lineNumbers, dc)
		}

		labelH := round(fontSize * 1.3)
		editH := labelH
		buttonW, buttonH := labelH*3, labelH+5
		margin := 10
		col0x := margin
		col0w := max(300, 2*buttonW+10)
		col1x := col0x + col0w + margin
		col1w := width - col1x - margin
		row0y := margin
		row1y := row0y + labelH
		startButtonX := col0x + (col0w-buttonW-margin)/2
		projectsY := row0y + labelH
		projectsH := height - 2*margin - buttonH - projectsY
		startButtonY := projectsY + projectsH + margin
		inputY := height - margin - editH
		outputH := 200
		outputY := inputY - margin - outputH
		codeY := row1y
		codeH := outputY - margin - codeY
		codeEditX := col1x + numberW + 1
		codeEditW := col1w - numberW - 1
		scrollBarH := w32.GetSystemMetrics(w32.SM_CYHSCROLL)

		setPos(projectsCaption, col0x, row0y, col0w, labelH)
		setPos(projectTree, col0x, projectsY, col0w, projectsH)
		setPos(startButton, startButtonX, startButtonY, buttonW, buttonH)
		setPos(codeCaption, codeEditX, row0y, col1w, labelH)
		setPos(lineNumbers, col1x, codeY+3, numberW, codeH-int(scrollBarH)-6)
		setPos(codeEdit, codeEditX, codeY, codeEditW, codeH)
		setPos(consoleOutput, col1x, outputY, col1w, outputH)
		setPos(consoleInput, col1x, inputY, col1w, editH)
		updateLineNumbers()

		w32.InvalidateRect(window, nil, true)
	}

	outputBuf := newSyncBuffer()

	startProgram := func() {
		if openFilePath == "" {
			return
		}

		// TODO This function might create new files, like go.mod, so update
		// the file tree afterwards.

		programRunning = true
		w32.SendMessage(window, programStartMessage, 0, 0)

		ctx, cancel := context.WithCancel(context.Background())
		stopProgram = cancel

		go func(ctx context.Context) {
			defer func() {
				programMu.Lock()
				programRunning = false
				cancel = func() {}
				w32.SendMessage(window, programStopMessage, 0, 0)
				programMu.Unlock()
			}()

			code, err := w32.GetWindowText(codeEdit)
			if err != nil {
				fmt.Fprintf(outputBuf, "Unable to read code: %s\r\n", err)
				return
			}

			projectsPath, err := projectsDir()
			if err != nil {
				fmt.Fprintf(outputBuf,
					"Unable to read projects path: %s\r\n", err)
				return
			}

			openFileFolder, _ := filepath.Split(openFilePath)
			projectName := filepath.Base(openFileFolder)
			projectPath := filepath.Join(projectsPath, projectName)

			if isDone(ctx) {
				return
			}

			code = strings.ReplaceAll(code, "\r\n", "\n")
			if err := os.WriteFile(openFilePath, []byte(code), 0666); err != nil {
				fmt.Fprintf(outputBuf,
					"Unable to write file '%s': %s\r\n", openFilePath, err)
				return
			}

			exeFilePath := filepath.Join(projectPath, projectName+".exe")

			modFilePath := filepath.Join(projectPath, "go.mod")
			if !pathExists(modFilePath) {
				init := exec.CommandContext(ctx, "go", "mod", "init", projectName)
				init.Dir = projectPath
				output, err := init.CombinedOutput()
				if isDone(ctx) {
					return
				}
				if err != nil {
					fmt.Fprintf(outputBuf,
						"go mod init failed: %s\r\n%s\r\n", err, output)
					return
				}
			}

			// TODO Always run go mod tidy? Or only on error?
			tidy := exec.CommandContext(ctx, "go", "mod", "tidy")
			tidy.Dir = projectPath
			output, err := tidy.CombinedOutput()
			if isDone(ctx) {
				return
			}
			if err != nil {
				fmt.Fprintf(outputBuf,
					"go mod tidy failed: %s\r\n%s\r\n", err, output)
				return
			}

			build := exec.CommandContext(ctx, "go", "build", "-o", exeFilePath, ".")
			build.Dir = projectPath
			output, err = build.CombinedOutput()
			if isDone(ctx) {
				return
			}
			if err != nil {
				fmt.Fprintf(outputBuf,
					"go build failed: %s\r\n%s\r\n", err, output)
				return
			}

			execute := exec.CommandContext(ctx, exeFilePath)
			execute.Dir = projectPath
			execute.Stdout = outputBuf
			execute.Stderr = outputBuf
			programStdin, err = execute.StdinPipe()
			if err != nil {
				fmt.Fprintf(outputBuf,
					"unable to get program's stdin: %s\r\n", err)
			}
			err = execute.Run()
			if err != nil {
				fmt.Fprintf(outputBuf, "program failed: %s\r\n", err)
			}
		}(ctx)
	}

	readCodeFromRepo := func() (string, error) {
		const gitURL = `https://github.com/gonutz/gool_exchange.git`

		tempDir, err := ioutil.TempDir("", "gool_exchange_*")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(tempDir)

		output, err := exec.Command("git", "clone", gitURL, tempDir).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf(err.Error() + ": " + string(output))
		}

		content, err := os.ReadFile(filepath.Join(tempDir, "main.go"))
		if err != nil {
			return "", err
		}

		code := string(content)
		code = strings.ReplaceAll(code, "\n", "\r\n")
		return code, nil
	}

	synchCodeWithRepo := func() {
		if answer, err := w32.MessageBox(
			window,
			w32.String("Soll der Code mit der Online-Version synchronisiert werden?"),
			w32.String("Sicher?"),
			w32.MB_YESNO|w32.MB_TOPMOST|w32.MB_ICONQUESTION,
		); err != nil || answer != w32.IDYES {
			return
		}

		code, err := readCodeFromRepo()
		if err != nil {
			w32.MessageBox(
				window,
				w32.String(err.Error()),
				w32.String("Synchronisation fehlgeschlagen"),
				w32.MB_OK|w32.MB_TOPMOST|w32.MB_ICONERROR,
			)
		} else {
			w32.SetWindowText(codeEdit, w32.String(code))
			w32.MessageBox(
				window,
				w32.String("Der Code wurde synchronisiert."),
				w32.String("Synchronisation erfolgreich"),
				w32.MB_OK|w32.MB_TOPMOST|w32.MB_ICONINFORMATION,
			)
		}
	}

	onStartButtonClick := func() {
		programMu.Lock()
		defer programMu.Unlock()

		if programRunning {
			stopProgram()
		} else {
			startProgram()
		}
	}

	w32.SetWindowSubclass(
		consoleInput,
		w32.NewWindowSubclassProc(func(
			window w32.HWND,
			message uint32,
			w, l, subclassID, refData uintptr,
		) uintptr {
			if message == w32.WM_KEYDOWN && w == w32.VK_RETURN {
				input, err := w32.GetWindowText(consoleInput)
				if err == nil {
					programStdin.Write([]byte(input + "\r\n"))
				}
				w32.SetWindowText(consoleInput, nil)
			}
			return w32.DefSubclassProc(window, message, w, l)
		}),
		0,
		0,
	)

	readConsoleOutput := func() {
		latest := outputBuf.Flush()
		if len(latest) > 0 {
			text, err := w32.GetWindowText(consoleOutput)
			if err == nil {
				text += strings.ReplaceAll(string(latest), "\n", "\r\n")
				w32.SetWindowText(consoleOutput, w32.String(text))
				w32.SendMessage(consoleOutput, w32.EM_LINESCROLL, 0, 9999999)
			}
		}
	}

	updateFonts := func() error {
		if fontSize < minFontSize {
			fontSize = minFontSize
		}
		if fontSize > maxFontSize {
			fontSize = maxFontSize
		}

		fontHeight := int32(round(-fontSize))
		tahomaDesc := w32.LOGFONT{
			Height:         fontHeight,
			Weight:         w32.FW_NORMAL,
			CharSet:        w32.DEFAULT_CHARSET,
			OutPrecision:   w32.OUT_CHARACTER_PRECIS,
			ClipPrecision:  w32.CLIP_CHARACTER_PRECIS,
			Quality:        w32.DEFAULT_QUALITY,
			PitchAndFamily: w32.DEFAULT_PITCH | w32.FF_DONTCARE,
		}
		w32.SetString(tahomaDesc.FaceName[:], "Tahoma")
		labelFont, err = w32.CreateFontIndirect(&tahomaDesc)
		if err != nil {
			return err
		}

		codeFontDesc := w32.LOGFONT{
			Height:         fontHeight,
			Weight:         w32.FW_NORMAL,
			CharSet:        w32.DEFAULT_CHARSET,
			OutPrecision:   w32.OUT_CHARACTER_PRECIS,
			ClipPrecision:  w32.CLIP_CHARACTER_PRECIS,
			Quality:        w32.DEFAULT_QUALITY,
			PitchAndFamily: w32.DEFAULT_PITCH | w32.FF_DONTCARE,
		}
		w32.SetString(codeFontDesc.FaceName[:], "Courier New")
		codeFont, err = w32.CreateFontIndirect(&codeFontDesc)
		if err != nil {
			return err
		}

		w32.SendMessage(projectsCaption, w32.WM_SETFONT, uintptr(labelFont), 1)
		w32.SendMessage(projectTree, w32.WM_SETFONT, uintptr(labelFont), 1)
		w32.SendMessage(startButton, w32.WM_SETFONT, uintptr(labelFont), 1)
		w32.SendMessage(codeCaption, w32.WM_SETFONT, uintptr(labelFont), 1)
		w32.SendMessage(codeEdit, w32.WM_SETFONT, uintptr(codeFont), 1)
		w32.SendMessage(lineNumbers, w32.WM_SETFONT, uintptr(codeFont), 1)
		w32.SendMessage(consoleOutput, w32.WM_SETFONT, uintptr(codeFont), 1)
		w32.SendMessage(consoleInput, w32.WM_SETFONT, uintptr(codeFont), 1)

		tabWidth := 4 * 4
		w32.SendMessage(codeEdit, w32.EM_SETTABSTOPS, 1, uintptr(unsafe.Pointer(&tabWidth)))

		layoutControls()

		return nil
	}

	incFontSize := func() error {
		fontSize += 2
		return updateFonts()
	}

	decFontSize := func() error {
		fontSize -= 2
		return updateFonts()
	}

	if err := updateFonts(); err != nil {
		return err
	}

	openFile := func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		openFilePath = path

		code := string(data)
		code = strings.ReplaceAll(code, "\r", "")
		code = strings.ReplaceAll(code, "\n", "\r\n")
		w32.ShowWindow(lineNumbers, w32.SW_SHOW)
		w32.EnableWindow(codeEdit, true)
		w32.EnableWindow(startButton, true)
		w32.SetWindowText(codeEdit, w32.String(code))
		w32.SetWindowText(window, w32.String("Gool - "+path))
		layoutControls()

		return nil
	}

	var fillTree func(parent, prev w32.HTREEITEM, f *folder, itemToPath map[w32.HTREEITEM]string) error
	fillTree = func(parent, prev w32.HTREEITEM, f *folder, itemToPath map[w32.HTREEITEM]string) error {
		for _, folder := range f.folders {
			var err error
			prev, err = w32.TreeView_InsertItem(projectTree, &w32.TVINSERTSTRUCT{
				Parent:      parent,
				InsertAfter: prev,
				ItemEx: w32.TVITEMEX{
					Mask: w32.TVIF_TEXT,
					Text: w32.String(filepath.Base(folder.path)),
				},
			})
			if err != nil {
				return err
			}

			if err := fillTree(prev, w32.TVI_ROOT, folder, itemToPath); err != nil {
				return err
			}
		}

		for _, file := range f.files {
			var err error
			prev, err = w32.TreeView_InsertItem(projectTree, &w32.TVINSERTSTRUCT{
				Parent:      parent,
				InsertAfter: prev,
				ItemEx: w32.TVITEMEX{
					Mask: w32.TVIF_TEXT,
					Text: w32.String(filepath.Base(file)),
				},
			})
			if err != nil {
				return err
			}
			itemToPath[prev] = file
		}

		return nil
	}

	fileTreeItemToPath := map[w32.HTREEITEM]string{}

	updateProjects := func() error {
		projects, err := projectsDir()
		if err != nil {
			return err
		}

		tree, err := readProjectTree(projects)
		if err != nil {
			return err
		}

		w32.TreeView_DeleteAllItems(projectTree)
		fileTreeItemToPath = map[w32.HTREEITEM]string{}
		return fillTree(0, w32.TVI_ROOT, tree, fileTreeItemToPath)
	}

	if err := updateProjects(); err != nil {
		return err
	}

	type settings struct {
		FontSize float64
		OpenFile string
	}

	settingsPath := func() string {
		return filepath.Join(os.Getenv("APPDATA"), "gool.settings")
	}

	onClose := func() error {
		s := settings{
			FontSize: fontSize,
			OpenFile: openFilePath,
		}
		data, err := json.Marshal(s)
		if err != nil {
			return err
		}
		return os.WriteFile(settingsPath(), data, 0666)
	}

	if fileToOpen != "" {
		openFile(fileToOpen)
	}

	if data, err := os.ReadFile(settingsPath()); err == nil {
		var s settings
		if json.Unmarshal(data, &s) == nil {
			fontSize = s.FontSize
			updateFonts()
			if pathExists(s.OpenFile) {
				openFile(s.OpenFile)
			}
		}
	}

	handleMessage = func(window w32.HWND, message uint32, w, l uintptr) uintptr {
		switch message {
		case w32.WM_TIMER:
			switch w {
			case programTimerID:
				readConsoleOutput()
			case scrollCheckTimerID:
				topCodeLine := w32.Edit_GetFirstVisibleLine(codeEdit)
				if topCodeLine != lastTopCodeLine {
					lastTopCodeLine = topCodeLine
					updateLineNumbers()
				}
				if w32.GetAsyncKeyState(w32.VK_LBUTTON)&0x8000 == 0 {
					w32.KillTimer(window, scrollCheckTimerID)
				}
			}
			return 0
		case w32.WM_COMMAND:
			lowW := w & 0xFFFF
			highW := (w & 0xFFFF0000) >> 16
			if lowW == startButtonID && l == uintptr(startButton) {
				onStartButtonClick()
			}
			if highW == 1 && l == 0 && lowW == synchCodeWithRepoID {
				synchCodeWithRepo()
			}
			if highW == 1 && l == 0 && lowW == startButtonShortcutID {
				onStartButtonClick()
			}
			if highW == 1 && l == 0 && lowW == refreshShortcutID {
				updateProjects()
			}
			if highW == 1 && l == 0 && lowW == largerFontShortcutID {
				incFontSize()
			}
			if highW == 1 && l == 0 && lowW == smallerFontShortcutID {
				decFontSize()
			}
			if highW == 1 && l == 0 && lowW == fileExplorerShortcutID {
				dir := filepath.Dir(openFilePath)
				if openFilePath == "" {
					dir, _ = projectsDir()
				}
				exec.Command("cmd", "/C", "start", dir).Start()
			}
			if highW == 1 && l == 0 && lowW == commandLineShortcutID {
				cmd := exec.Command("cmd", "/C", "start", "/MAX", "cmd")
				cmd.Dir = filepath.Dir(openFilePath)
				if openFilePath == "" {
					cmd.Dir, _ = projectsDir()
				}
				cmd.Start()
			}
			if highW == w32.EN_VSCROLL && l == uintptr(codeEdit) {
				updateLineNumbers()
			}
			if highW == w32.EN_CHANGE && l == uintptr(codeEdit) {
				lineCount := int(w32.Edit_GetLineCount(codeEdit))
				if lineCount != lastLineCount {
					if len(strconv.Itoa(lineCount)) != len(strconv.Itoa(lastLineCount)) {
						// The line numbers have grown or shrunk in size, so we
						// need to adjust the line number column width.
						layoutControls()
					} else {
						updateLineNumbers()
					}
					lastLineCount = lineCount
				}
			}
			return 0
		case w32.WM_PARENTNOTIFY:
			if w&0xFFFF == w32.WM_LBUTTONDOWN {
				r, _ := w32.GetWindowRect(codeEdit)
				offset, _ := w32.ClientToScreen(window, w32.POINT{})
				x := int32(int16(l&0xFFFF)) + offset.X
				y := int32(int16((l&0xFFFF0000)>>16)) + offset.Y
				scrollBarW := w32.GetSystemMetrics(w32.SM_CXHSCROLL)
				left := r.Right - scrollBarW - 3 // -3 to be safe.
				if left <= x && x < r.Right &&
					r.Top <= y && y < r.Bottom {
					lastTopCodeLine = w32.Edit_GetFirstVisibleLine(codeEdit)
					w32.SetTimer(window, scrollCheckTimerID, 10, 0)
				}
			}
			return w32.DefWindowProc(window, message, w, l)
		case programStartMessage:
			w32.SetWindowText(startButton, w32.String("Stopp"))
			w32.SetWindowText(consoleOutput, nil)
			w32.SetWindowText(consoleInput, nil)
			w32.EnableWindow(consoleInput, true)
			w32.SetFocus(consoleInput)
			w32.SetTimer(window, programTimerID, 50, 0)
			return 0
		case programStopMessage:
			w32.KillTimer(window, programTimerID)
			readConsoleOutput()
			w32.SetWindowText(startButton, w32.String("Start"))
			w32.SetFocus(codeEdit)
			w32.EnableWindow(consoleInput, false)
			w32.SetWindowText(consoleInput, w32.String("Programm-Input"))
			return 0
		case w32.WM_MOUSEWHEEL:
			delta := int16((w & 0xFFFF0000) >> 16)
			flags := w & 0xFFFF
			if flags == w32.MK_CONTROL {
				d := float64(delta) / 120.0
				fontSize *= math.Pow(1.1, d)
				updateFonts()
			}
			return 0
		case w32.WM_SIZE:
			layoutControls()
			return 0
		case w32.WM_ACTIVATE:
			if w&0xFFFF != w32.WA_INACTIVE {
				// TODO Update the file tree in a diff way, do not delete and
				// re-create everything, because it will collapse all nodes and
				// unselect the last selection.
				// updateProjects()
			}
			return 0
		case w32.WM_NOTIFY:
			header := *(*w32.NMHDR)(unsafe.Pointer(l))
			if header.Code == w32.NM_DBLCLK {
				item := w32.TreeView_GetSelection(projectTree)
				path := fileTreeItemToPath[item]
				if strings.HasSuffix(strings.ToLower(path), ".go") {
					if err := openFile(path); err != nil {
						w32.MessageBox(
							0,
							w32.String(err.Error()),
							w32.String("Error"),
							w32.MB_ICONERROR|w32.MB_OK|w32.MB_TOPMOST,
						)
					}
				} else if path != "" {
					exec.Command("cmd", "/C", "start", path).Start()
				}
			}
			return 0
		case w32.WM_CLOSE:
			onClose()
			return w32.DefWindowProc(window, message, w, l)
		case w32.WM_DESTROY:
			w32.PostQuitMessage(0)
			return 0
		default:
			return w32.DefWindowProc(window, message, w, l)
		}
	}

	shortcuts, err := w32.CreateAcceleratorTable([]w32.ACCEL{
		{
			Virt: w32.FVIRTKEY,
			Key:  w32.VK_F5,
			Cmd:  refreshShortcutID,
		},
		{
			Virt: w32.FVIRTKEY,
			Key:  w32.VK_F2,
			Cmd:  synchCodeWithRepoID,
		},
		{
			Virt: w32.FVIRTKEY,
			Key:  w32.VK_F9,
			Cmd:  startButtonShortcutID,
		},
		{
			Virt: w32.FVIRTKEY,
			Key:  w32.VK_F11,
			Cmd:  fileExplorerShortcutID,
		},
		{
			Virt: w32.FVIRTKEY,
			Key:  w32.VK_F12,
			Cmd:  commandLineShortcutID,
		},
		{
			Virt: w32.FVIRTKEY | w32.FCONTROL,
			Key:  w32.VK_ADD,
			Cmd:  largerFontShortcutID,
		},
		{
			Virt: w32.FVIRTKEY | w32.FCONTROL,
			Key:  w32.VK_OEM_PLUS,
			Cmd:  largerFontShortcutID,
		},
		{
			Virt: w32.FVIRTKEY | w32.FCONTROL,
			Key:  w32.VK_SUBTRACT,
			Cmd:  smallerFontShortcutID,
		},
		{
			Virt: w32.FVIRTKEY | w32.FCONTROL,
			Key:  w32.VK_OEM_MINUS,
			Cmd:  smallerFontShortcutID,
		},
	})
	if err != nil {
		return err
	}
	defer w32.DestroyAcceleratorTable(shortcuts)

	for {
		var msg w32.MSG
		ok, err := w32.GetMessage(&msg, 0, 0, 0)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		if w32.TranslateAccelerator(window, shortcuts, &msg) != nil {
			w32.TranslateMessage(&msg)
			w32.DispatchMessage(&msg)
		}
	}

	return nil
}

// TODO Include this in the resource file.
func setManifest() error {
	manifest := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
    <dependency>
        <dependentAssembly>
            <assemblyIdentity
				type="win32"
				processorArchitecture="*"
				language="*"
				name="Microsoft.Windows.Common-Controls"
				version="6.0.0.0"
				publicKeyToken="6595b64144ccf1df"
			/>
        </dependentAssembly>
    </dependency>
</assembly>`

	// Create a temporary manifest file, load it, then delete it.
	f, err := ioutil.TempFile("", "manifest_")
	if err != nil {
		return err
	}
	manifestPath := f.Name()
	defer os.Remove(manifestPath)

	f.WriteString(manifest)
	f.Close()

	ctx, err := w32.CreateActCtx(&w32.ACTCTX{
		Source: w32.String(manifestPath),
	})
	if err != nil {
		return err
	}

	_, err = w32.ActivateActCtx(ctx)
	return err
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}

func newSyncBuffer() *syncBuffer {
	return &syncBuffer{}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncBuffer) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	w.buf.Write(p)
	w.mu.Unlock()
	return len(p), nil
}

func (w *syncBuffer) Flush() []byte {
	w.mu.Lock()
	b := append([]byte{}, w.buf.Bytes()...)
	w.buf.Reset()
	w.mu.Unlock()
	return b
}

func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// hideConsoleWindow hides the associated console window that gets created for
// Windows applications that are of type console instead of type GUI. When
// building you can pass the ldflag H=windowsgui to suppress this but if you
// just go build or go run, a console window will pop open along with the GUI
// window. hideConsoleWindow hides it.
func hideConsoleWindow() {
	console := w32.GetConsoleWindow()
	if console == 0 {
		return // No console attached.
	}
	// If this application is the process that created the console window, then
	// this program was not compiled with the -H=windowsgui flag and on start-up
	// it created a console along with the main application window. In this case
	// hide the console window. See
	// http://stackoverflow.com/questions/9009333/how-to-check-if-the-program-is-run-from-a-console
	_, consoleProcID, _ := w32.GetWindowThreadProcessId(console)
	if w32.GetCurrentProcessId() == consoleProcID {
		w32.ShowWindowAsync(console, w32.SW_HIDE)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func round(x float64) int {
	if x < 0 {
		return int(x - 0.5)
	}
	return int(x + 0.5)
}

func numberRange(from, to int) string {
	var s string
	for i := from; i <= to; i++ {
		s += strconv.Itoa(i) + "\r\n"
	}
	return s
}

func readProjectTree(root string) (*folder, error) {
	folder, err := readFolder(root)
	if err != nil {
		return nil, err
	}
	// For the root folder we do not show files.
	folder.files = nil
	return folder, nil
}

type folder struct {
	path    string
	folders []*folder
	files   []string
}

func readFolder(path string) (*folder, error) {
	files, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	folder := &folder{path: path}

	for _, file := range files {
		if strings.HasPrefix(file.Name(), ".") {
			continue
		}

		subPath := filepath.Join(path, file.Name())
		if file.IsDir() {
			sub, err := readFolder(subPath)
			if err != nil {
				return nil, err
			}
			folder.folders = append(folder.folders, sub)
		} else {
			folder.files = append(folder.files, subPath)
		}
	}

	return folder, nil
}

const helloWorldCode = `package main

import "fmt"

func main() {
	fmt.Println("Hello World!")
}
`
