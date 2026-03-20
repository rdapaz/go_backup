package agent

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// UpdateTaskScheduler creates/updates/removes Windows Task Scheduler entries
// based on the local schedules database.
func (a *Agent) UpdateTaskScheduler() error {
	schedules, err := a.config.GetLocalSchedules()
	if err != nil {
		return fmt.Errorf("get local schedules: %w", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	exePath, _ = filepath.Abs(exePath)

	configDBPath := a.config.Get("config_db_path", "")

	// Get existing GoBackup tasks
	existing, err := listGoBackupTasks()
	if err != nil {
		log.Printf("[scheduler] warning: could not list existing tasks: %v", err)
	}

	activeTaskNames := make(map[string]bool)

	for _, sched := range schedules {
		taskName := fmt.Sprintf("GoBackup_%d", sched.ID)
		activeTaskNames[taskName] = true

		if !sched.Enabled {
			// Delete disabled tasks
			if existing[taskName] {
				deleteTask(taskName)
			}
			continue
		}

		// Convert cron to schtasks schedule
		schedType, schedMod := cronToSchtasks(sched.CronExpr)
		if schedType == "" {
			log.Printf("[scheduler] unsupported cron expression for schedule %d: %s", sched.ID, sched.CronExpr)
			continue
		}

		args := fmt.Sprintf(`agent run-schedule --id %d --config-db "%s"`, sched.ID, configDBPath)
		cmdLine := fmt.Sprintf(`"%s" %s`, exePath, args)

		cmdArgs := []string{
			"/Create", "/F",
			"/TN", taskName,
			"/TR", cmdLine,
			"/SC", schedType,
		}
		if schedMod != "" {
			cmdArgs = append(cmdArgs, "/MO", schedMod)
		}
		cmdArgs = append(cmdArgs, "/ST", cronStartTime(sched.CronExpr))

		cmd := exec.Command("schtasks.exe", cmdArgs...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("[scheduler] failed to create task %s: %v\n%s", taskName, err, output)
			continue
		}
		log.Printf("[scheduler] created/updated task: %s", taskName)
	}

	// Remove tasks for schedules that no longer exist
	for name := range existing {
		if !activeTaskNames[name] {
			deleteTask(name)
		}
	}

	return nil
}

// listGoBackupTasks returns a set of existing GoBackup_* task names.
func listGoBackupTasks() (map[string]bool, error) {
	cmd := exec.Command("schtasks.exe", "/Query", "/FO", "LIST")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	tasks := make(map[string]bool)
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "TaskName:") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "TaskName:"))
			name = filepath.Base(name) // strip path prefix
			if strings.HasPrefix(name, "GoBackup_") {
				tasks[name] = true
			}
		}
	}
	return tasks, nil
}

func deleteTask(name string) {
	cmd := exec.Command("schtasks.exe", "/Delete", "/TN", name, "/F")
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[scheduler] failed to delete task %s: %v\n%s", name, err, output)
	} else {
		log.Printf("[scheduler] deleted task: %s", name)
	}
}

// cronToSchtasks converts a cron expression to schtasks /SC and /MO values.
// Supports common patterns. Returns ("", "") for unsupported expressions.
func cronToSchtasks(cron string) (schedType, modifier string) {
	parts := strings.Fields(cron)
	if len(parts) != 5 {
		return "", ""
	}

	minute, hour, dom, _, dow := parts[0], parts[1], parts[2], parts[3], parts[4]
	_ = minute // Used only for start time

	// Daily: "0 2 * * *"
	if dom == "*" && dow == "*" && !strings.Contains(hour, "/") && !strings.Contains(hour, ",") {
		return "DAILY", ""
	}

	// Every N days: "0 2 */N * *"
	if strings.HasPrefix(dom, "*/") && dow == "*" {
		n := strings.TrimPrefix(dom, "*/")
		return "DAILY", n
	}

	// Weekly: "0 3 * * 0" (specific day of week)
	if dom == "*" && dow != "*" && !strings.Contains(dow, ",") && !strings.Contains(dow, "-") {
		dayMap := map[string]string{
			"0": "SUN", "1": "MON", "2": "TUE", "3": "WED",
			"4": "THU", "5": "FRI", "6": "SAT", "7": "SUN",
		}
		if day, ok := dayMap[dow]; ok {
			return "WEEKLY", day
		}
	}

	// Monthly on specific days: "0 2 1 * *" or "0 2 1,15 * *"
	if dom != "*" && dow == "*" {
		return "MONTHLY", dom
	}

	// Hourly: "0 * * * *" or "0 */N * * *"
	if hour == "*" && dom == "*" && dow == "*" {
		return "HOURLY", ""
	}
	if strings.HasPrefix(hour, "*/") && dom == "*" && dow == "*" {
		n := strings.TrimPrefix(hour, "*/")
		return "HOURLY", n
	}

	return "", ""
}

// cronStartTime extracts the start time (HH:MM) from a cron expression.
func cronStartTime(cron string) string {
	parts := strings.Fields(cron)
	if len(parts) < 2 {
		return "02:00"
	}

	minute := parts[0]
	hour := parts[1]

	// Handle wildcards and intervals
	if hour == "*" || strings.HasPrefix(hour, "*/") {
		hour = "0"
	}
	if minute == "*" || strings.HasPrefix(minute, "*/") {
		minute = "0"
	}

	// Handle comma-separated (take first)
	if idx := strings.IndexByte(hour, ','); idx >= 0 {
		hour = hour[:idx]
	}
	if idx := strings.IndexByte(minute, ','); idx >= 0 {
		minute = minute[:idx]
	}

	return fmt.Sprintf("%02s:%02s", padZero(hour), padZero(minute))
}

func padZero(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}
