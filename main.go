package main

// TODO On close, stop the running program, if any.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/gonutz/w32/v3"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

const (
	projectsID = 1 + iota
	newButtonID
	startButtonID
	startButtonShortcutID
	largerFontShortcutID
	smallerFontShortcutID
	programTimerID
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
		w32.WS_OVERLAPPEDWINDOW|w32.WS_VISIBLE|w32.WS_MAXIMIZE,
		w32.CW_USEDEFAULT, w32.CW_USEDEFAULT, w32.CW_USEDEFAULT, w32.CW_USEDEFAULT,
		0, 0, 0,
		nil,
	)
	if err != nil {
		return err
	}

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

	projects, err := w32.CreateWindowEx(
		0,
		w32.WC_TREEVIEW,
		nil,
		w32.WS_VISIBLE|w32.WS_CHILD|w32.WS_BORDER|
			w32.TVS_HASLINES|w32.TVS_HASBUTTONS|w32.TVS_LINESATROOT,
		10, 40, 200, 200,
		window,
		projectsID,
		0,
		nil,
	)

	newButton, err := w32.CreateWindowEx(
		0,
		w32.String("BUTTON"),
		w32.String("Neu"),
		// TODO Enable this, once it works.
		w32.WS_VISIBLE|w32.WS_CHILD|w32.WS_DISABLED,
		10, 300, 80, 25,
		window,
		newButtonID, 0, nil,
	)
	if err != nil {
		return err
	}

	startButton, err := w32.CreateWindowEx(
		0,
		w32.String("BUTTON"),
		w32.String("Start"),
		w32.WS_VISIBLE|w32.WS_CHILD,
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

	helloWordCode := strings.ReplaceAll(strings.TrimSpace(`
package main

import "fmt"

func main() {
	fmt.Println("Hello World!")
}
`), "\n", "\r\n")

	codeEdit, err := w32.CreateWindowEx(
		w32.WS_EX_CLIENTEDGE,
		w32.String("EDIT"),
		w32.String(helloWordCode),
		w32.WS_VISIBLE|w32.WS_CHILD|w32.ES_MULTILINE|w32.ES_WANTRETURN|
			w32.WS_HSCROLL|w32.ES_AUTOHSCROLL|w32.WS_VSCROLL|w32.ES_AUTOVSCROLL,
		220, 40, 300, 300,
		window,
		0, 0, nil,
	)
	if err != nil {
		return err
	}
	w32.SetFocus(codeEdit)

	consoleOutput, err := w32.CreateWindowEx(
		w32.WS_EX_CLIENTEDGE,
		w32.String("EDIT"),
		w32.String("Console output..."),
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
		nil,
		w32.WS_VISIBLE|w32.WS_CHILD|w32.WS_DISABLED,
		220, 430, 300, 25,
		window,
		0, 0, nil,
	)
	if err != nil {
		return err
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
		newButtonX := col0x + (col0w-2*buttonW-margin)/2
		startButtonX := newButtonX + buttonW + margin
		projectsY := row0y + labelH
		projectsH := height - 2*margin - buttonH - projectsY
		newButtonY := projectsY + projectsH + margin
		startButtonY := projectsY + projectsH + margin
		inputY := height - margin - editH
		outputH := 200
		outputY := inputY - margin - outputH
		codeY := row1y
		codeH := outputY - margin - codeY

		setPos(projectsCaption, col0x, row0y, col0w, labelH)
		setPos(projects, col0x, projectsY, col0w, projectsH)
		setPos(newButton, newButtonX, newButtonY, buttonW, buttonH)
		setPos(startButton, startButtonX, startButtonY, buttonW, buttonH)
		setPos(codeCaption, col1x, row0y, col1w, labelH)
		setPos(codeEdit, col1x, codeY, col1w, codeH)
		setPos(consoleOutput, col1x, outputY, col1w, outputH)
		setPos(consoleInput, col1x, inputY, col1w, editH)

		w32.InvalidateRect(window, nil, true)
	}

	projectsDir := func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}

		return filepath.Join(filepath.Dir(exe), "gool_projects"), nil
	}

	currentProjectName := func() string {
		// TODO Read projects tree, use selected thingy.
		return "hello_world"
	}

	currentFileName := func() string {
		// TODO Read projects tree, use selected thingy.
		return "main.go"
	}

	currentFilePath := func() string {
		dir, err := projectsDir()
		if err != nil {
			return "" // TODO
		}
		return filepath.Join(dir, currentProjectName(), currentFileName())
	}

	outputBuf := newSyncBuffer()

	var (
		programMu      sync.Mutex
		programRunning bool
		stopProgram    = func() {}
		programStdin   io.WriteCloser
	)

	startProgram := func() {
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

			projectName := currentProjectName()
			projectPath := filepath.Join(projectsPath, projectName)

			os.MkdirAll(projectPath, 0666) // Ignore errors on purpose.

			if isDone(ctx) {
				return
			}

			goFilePath := filepath.Join(projectPath, currentFileName())
			if err := os.WriteFile(goFilePath, []byte(code), 0666); err != nil {
				fmt.Fprintf(outputBuf,
					"Unable to write file '%s': %s\r\n", goFilePath, err)
				return
			}

			exeFilePath := filepath.Join(projectPath, projectName+".exe")

			modFilePath := filepath.Join(projectPath, "go.mod")
			if !fileExists(modFilePath) {
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

	onStartButtonClick := func() {
		programMu.Lock()
		defer programMu.Unlock()

		if programRunning {
			stopProgram()
		} else {
			startProgram()
		}
	}

	onNewButtonClick := func() {
		// TODO
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
		tahoma, err := w32.CreateFontIndirect(&tahomaDesc)
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
		codeFont, err := w32.CreateFontIndirect(&codeFontDesc)
		if err != nil {
			return err
		}

		w32.SendMessage(projectsCaption, w32.WM_SETFONT, uintptr(tahoma), 1)
		w32.SendMessage(projects, w32.WM_SETFONT, uintptr(tahoma), 1)
		w32.SendMessage(newButton, w32.WM_SETFONT, uintptr(tahoma), 1)
		w32.SendMessage(startButton, w32.WM_SETFONT, uintptr(tahoma), 1)
		w32.SendMessage(codeCaption, w32.WM_SETFONT, uintptr(tahoma), 1)
		w32.SendMessage(codeEdit, w32.WM_SETFONT, uintptr(codeFont), 1)
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

	if data, err := os.ReadFile(currentFilePath()); err == nil {
		code := string(data)
		code = strings.ReplaceAll(code, "\r", "")
		code = strings.ReplaceAll(code, "\n", "\r\n")
		w32.SetWindowText(codeEdit, w32.String(code))
	}

	handleMessage = func(window w32.HWND, message uint32, w, l uintptr) uintptr {
		switch message {
		case w32.WM_TIMER:
			readConsoleOutput()
			return 0
		case w32.WM_COMMAND:
			low := w & 0xFFFF
			high := (w & 0xFFFF0000) >> 16
			if low == startButtonID && l == uintptr(startButton) {
				onStartButtonClick()
			}
			if low == newButtonID && l == uintptr(newButton) {
				onNewButtonClick()
			}
			if high == 1 && l == 0 && low == startButtonShortcutID {
				onStartButtonClick()
			}
			if high == 1 && l == 0 && low == largerFontShortcutID {
				incFontSize()
			}
			if high == 1 && l == 0 && low == smallerFontShortcutID {
				decFontSize()
			}
			return 0
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
			Key:  w32.VK_F9,
			Cmd:  startButtonShortcutID,
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

func fileExists(path string) bool {
	stat, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if stat.IsDir() {
		return false
	}
	return true
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
