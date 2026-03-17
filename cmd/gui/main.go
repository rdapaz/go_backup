package main

import (
	"context"
	"fmt"
	"image/color"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"mybackup/core"
)

var crtGreen = color.RGBA{R: 0, G: 255, B: 65, A: 255}

func main() {
	a := app.NewWithID("com.gobackup.app")
	a.Settings().SetTheme(theme.DarkTheme())
	w := a.NewWindow("Go Backup")
	w.Resize(fyne.NewSize(780, 620))

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Backup", theme.UploadIcon(), makeBackupTab(a, w)),
		container.NewTabItemWithIcon("Restore", theme.DownloadIcon(), makeRestoreTab(w)),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	w.SetContent(tabs)
	w.ShowAndRun()
}

// crtLog creates a CRT-style green-on-black log display.
// Returns the container, an append function, and a clear function.
func crtLog() (fyne.CanvasObject, func(string), func()) {
	var lines []string

	list := widget.NewList(
		func() int { return len(lines) },
		func() fyne.CanvasObject {
			t := canvas.NewText("template", crtGreen)
			t.TextStyle = fyne.TextStyle{Monospace: true}
			t.TextSize = 13
			return t
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			t := obj.(*canvas.Text)
			if id < len(lines) {
				t.Text = lines[id]
			}
			t.Color = crtGreen
			t.Refresh()
		},
	)

	bg := canvas.NewRectangle(color.Black)
	logContainer := container.NewStack(bg, list)

	appendFn := func(msg string) {
		ts := time.Now().Format("15:04:05")
		line := fmt.Sprintf("[%s] %s", ts, msg)
		lines = append(lines, line)
		list.Refresh()
		list.ScrollToBottom()
	}

	clearFn := func() {
		lines = nil
		list.Refresh()
	}

	return logContainer, appendFn, clearFn
}

// ---------- Backup Tab ----------

func makeBackupTab(a fyne.App, w fyne.Window) fyne.CanvasObject {
	srcEntry := widget.NewEntry()
	srcEntry.SetPlaceHolder("Source directory")
	srcBrowse := widget.NewButtonWithIcon("Browse", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri != nil {
				srcEntry.SetText(uri.Path())
			}
		}, w)
	})

	dstEntry := widget.NewEntry()
	dstEntry.SetPlaceHolder("Destination directory")
	dstBrowse := widget.NewButtonWithIcon("Browse", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri != nil {
				dstEntry.SetText(uri.Path())
			}
		}, w)
	})

	profileSelect := widget.NewSelect(core.ValidProfiles, nil)
	profileSelect.SetSelected(core.ProfileDocuments)

	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("Leave empty to auto-generate")

	hintEntry := widget.NewEntry()
	hintEntry.SetPlaceHolder("Optional password hint")

	workersSlider := widget.NewSlider(1, float64(runtime.NumCPU()))
	workersSlider.Value = float64(runtime.NumCPU())
	workersSlider.Step = 1
	workersLabel := widget.NewLabel(fmt.Sprintf("Workers: %d", runtime.NumCPU()))
	workersSlider.OnChanged = func(v float64) {
		workersLabel.SetText(fmt.Sprintf("Workers: %d", int(v)))
	}

	keepStageCheck := widget.NewCheck("Keep staging directory", nil)

	// --- Blocklist ---
	// Load saved blocklist or use defaults
	prefs := a.Preferences()
	savedBlocklist := prefs.StringWithFallback("blocklist", "")
	var blocklist []string
	if savedBlocklist == "" {
		blocklist = append([]string{}, core.DefaultBlocklistDirs...)
	} else {
		blocklist = strings.Split(savedBlocklist, "\n")
	}

	blocklistBtn := widget.NewButtonWithIcon("Edit Blocklist...", theme.SettingsIcon(), nil)
	blocklistBtn.OnTapped = func() {
		entry := widget.NewMultiLineEntry()
		entry.SetText(strings.Join(blocklist, "\n"))
		entry.SetMinRowsVisible(15)
		entry.Wrapping = fyne.TextWrapOff

		resetBtn := widget.NewButton("Reset to Defaults", func() {
			entry.SetText(strings.Join(core.DefaultBlocklistDirs, "\n"))
		})

		content := container.NewBorder(
			widget.NewLabel("One directory name per line.\nThese directories will be skipped during scanning."),
			resetBtn,
			nil, nil,
			entry,
		)

		d := dialog.NewCustomConfirm("Blocklist — Directories to Skip", "Save", "Cancel", content, func(save bool) {
			if save {
				text := strings.TrimSpace(entry.Text)
				var cleaned []string
				for _, line := range strings.Split(text, "\n") {
					line = strings.TrimSpace(line)
					if line != "" {
						cleaned = append(cleaned, line)
					}
				}
				blocklist = cleaned
				prefs.SetString("blocklist", strings.Join(blocklist, "\n"))
			}
		}, w)
		d.Resize(fyne.NewSize(450, 450))
		d.Show()
	}

	// CRT-style log
	logContainer, appendLog, clearLog := crtLog()

	progress := widget.NewProgressBarInfinite()
	progress.Hide()

	var cancelFunc context.CancelFunc

	statusLabel := widget.NewLabel("Ready")

	startBtn := widget.NewButtonWithIcon("Start Backup", theme.MediaPlayIcon(), nil)
	cancelBtn := widget.NewButtonWithIcon("Cancel", theme.CancelIcon(), nil)
	cancelBtn.Disable()

	startBtn.OnTapped = func() {
		src := strings.TrimSpace(srcEntry.Text)
		dst := strings.TrimSpace(dstEntry.Text)
		if src == "" || dst == "" {
			dialog.ShowError(fmt.Errorf("source and destination are required"), w)
			return
		}
		profile := profileSelect.Selected
		if !core.IsValidProfile(profile) {
			dialog.ShowError(fmt.Errorf("select a valid profile"), w)
			return
		}

		cfg := core.BackupConfig{
			SrcDir:       src,
			DstDir:       dst,
			Profile:      profile,
			Password:     passwordEntry.Text,
			PasswordHint: hintEntry.Text,
			Workers:      int(workersSlider.Value),
			KeepStage:    keepStageCheck.Checked,
			Blocklist:    blocklist,
		}

		clearLog()
		progress.Show()
		startBtn.Disable()
		cancelBtn.Enable()
		statusLabel.SetText("Backing up...")

		var ctx context.Context
		ctx, cancelFunc = context.WithCancel(context.Background())

		go func() {
			result, err := core.RunBackup(ctx, cfg, func(msg string) {
				appendLog(msg)
			})

			progress.Hide()
			cancelBtn.Disable()
			startBtn.Enable()

			if err != nil {
				if ctx.Err() != nil {
					statusLabel.SetText("Cancelled")
					appendLog("Backup cancelled by user.")
				} else {
					statusLabel.SetText("Failed")
					appendLog(fmt.Sprintf("ERROR: %v", err))

					// If we have a staging directory, tell the user
					if result != nil && result.StageDirPath != "" {
						appendLog(fmt.Sprintf("Staging directory preserved at: %s", result.StageDirPath))
						appendLog("You can restore from this using the Restore tab → 'From Staging Directory'.")
						dialog.ShowInformation("Backup Failed — Staging Preserved",
							fmt.Sprintf("The backup failed but %d files were staged to:\n%s\n\nYou can restore from this directory using the Restore tab.",
								result.FileCount, result.StageDirPath), w)
					}
				}
				return
			}

			statusLabel.SetText("Complete")
			appendLog(fmt.Sprintf("Backup complete! %d files archived.", result.FileCount))
			appendLog(fmt.Sprintf("Archive: %s", result.ArchivePath))
			if cfg.Password == "" {
				appendLog(fmt.Sprintf("Generated password: %s", result.Password))
			}

			dialog.ShowInformation("Backup Complete",
				fmt.Sprintf("Archived %d files to:\n%s\n\nPassword: %s",
					result.FileCount, result.ArchivePath, result.Password), w)
		}()
	}

	cancelBtn.OnTapped = func() {
		if cancelFunc != nil {
			cancelFunc()
		}
	}

	// Layout
	form := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("Source:"), srcBrowse, srcEntry),
		container.NewBorder(nil, nil, widget.NewLabel("Destination:"), dstBrowse, dstEntry),
		container.NewBorder(nil, nil, widget.NewLabel("Profile:"), blocklistBtn, profileSelect),
		container.NewBorder(nil, nil, widget.NewLabel("Password:"), nil, passwordEntry),
		container.NewBorder(nil, nil, widget.NewLabel("Hint:"), nil, hintEntry),
		container.NewBorder(nil, nil, workersLabel, nil, workersSlider),
		keepStageCheck,
	)

	buttons := container.NewHBox(startBtn, cancelBtn, layout.NewSpacer(), statusLabel)

	return container.NewBorder(
		container.NewVBox(form, buttons, progress),
		nil, nil, nil,
		logContainer,
	)
}

// ---------- Restore Tab ----------

func makeRestoreTab(w fyne.Window) fyne.CanvasObject {
	modeSelect := widget.NewSelect([]string{"From Archive", "From Staging Directory"}, nil)
	modeSelect.SetSelected("From Archive")

	archiveEntry := widget.NewEntry()
	archiveEntry.SetPlaceHolder("Path to .7z archive")
	archiveBrowse := widget.NewButtonWithIcon("Browse", theme.FileIcon(), func() {
		dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
			if reader != nil {
				archiveEntry.SetText(reader.URI().Path())
				reader.Close()
			}
		}, w)
	})

	stageEntry := widget.NewEntry()
	stageEntry.SetPlaceHolder("Path to staging directory")
	stageBrowse := widget.NewButtonWithIcon("Browse", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri != nil {
				stageEntry.SetText(uri.Path())
			}
		}, w)
	})

	archiveRow := container.NewBorder(nil, nil, widget.NewLabel("Archive:"), archiveBrowse, archiveEntry)
	stageRow := container.NewBorder(nil, nil, widget.NewLabel("Stage Dir:"), stageBrowse, stageEntry)
	stageRow.Hide()

	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("Archive password")
	passwordRow := container.NewBorder(nil, nil, widget.NewLabel("Password:"), nil, passwordEntry)

	modeSelect.OnChanged = func(s string) {
		if s == "From Archive" {
			archiveRow.Show()
			passwordRow.Show()
			stageRow.Hide()
		} else {
			archiveRow.Hide()
			passwordRow.Hide()
			stageRow.Show()
		}
	}

	dstEntry := widget.NewEntry()
	dstEntry.SetPlaceHolder("Restore destination directory")
	dstBrowse := widget.NewButtonWithIcon("Browse", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri != nil {
				dstEntry.SetText(uri.Path())
			}
		}, w)
	})

	workersSlider := widget.NewSlider(1, float64(runtime.NumCPU()))
	workersSlider.Value = float64(runtime.NumCPU())
	workersSlider.Step = 1
	workersLabel := widget.NewLabel(fmt.Sprintf("Workers: %d", runtime.NumCPU()))
	workersSlider.OnChanged = func(v float64) {
		workersLabel.SetText(fmt.Sprintf("Workers: %d", int(v)))
	}

	// CRT-style log
	logContainer, appendLog, clearLog := crtLog()

	progress := widget.NewProgressBarInfinite()
	progress.Hide()

	statusLabel := widget.NewLabel("Ready")

	startBtn := widget.NewButtonWithIcon("Start Restore", theme.MediaPlayIcon(), nil)

	startBtn.OnTapped = func() {
		dst := strings.TrimSpace(dstEntry.Text)
		if dst == "" {
			dialog.ShowError(fmt.Errorf("destination is required"), w)
			return
		}

		var cfg core.RestoreConfig
		cfg.DstDir = dst
		cfg.Workers = int(workersSlider.Value)

		if modeSelect.Selected == "From Archive" {
			archive := strings.TrimSpace(archiveEntry.Text)
			pwd := passwordEntry.Text
			if archive == "" {
				dialog.ShowError(fmt.Errorf("archive path is required"), w)
				return
			}
			if pwd == "" {
				dialog.ShowError(fmt.Errorf("password is required for archive restore"), w)
				return
			}
			cfg.ArchivePath = archive
			cfg.Password = pwd
		} else {
			stage := strings.TrimSpace(stageEntry.Text)
			if stage == "" {
				dialog.ShowError(fmt.Errorf("staging directory is required"), w)
				return
			}
			cfg.StageDir = stage
		}

		clearLog()
		progress.Show()
		startBtn.Disable()
		statusLabel.SetText("Restoring...")

		go func() {
			result, err := core.RunRestore(cfg, func(msg string) {
				appendLog(msg)
			})

			progress.Hide()
			startBtn.Enable()

			if err != nil {
				statusLabel.SetText("Failed")
				appendLog(fmt.Sprintf("ERROR: %v", err))
				if result != nil {
					dialog.ShowError(fmt.Errorf("restored %d files with %d errors",
						result.FileCount, result.ErrorCount), w)
				} else {
					dialog.ShowError(err, w)
				}
				return
			}

			statusLabel.SetText("Complete")
			appendLog(fmt.Sprintf("Restore complete! %d files restored.", result.FileCount))
			dialog.ShowInformation("Restore Complete",
				fmt.Sprintf("Restored %d files to:\n%s", result.FileCount, dst), w)
		}()
	}

	// Layout
	form := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("Mode:"), nil, modeSelect),
		archiveRow,
		passwordRow,
		stageRow,
		container.NewBorder(nil, nil, widget.NewLabel("Destination:"), dstBrowse, dstEntry),
		container.NewBorder(nil, nil, workersLabel, nil, workersSlider),
	)

	buttons := container.NewHBox(startBtn, layout.NewSpacer(), statusLabel)

	return container.NewBorder(
		container.NewVBox(form, buttons, progress),
		nil, nil, nil,
		logContainer,
	)
}
