package core

import (
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

//go:embed app_update_helper.ps1
var appUpdateHelper []byte

func (e *nativeEngine) launchAppInstaller(state nativeUpdateState, rollback bool) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	slug := "hongguojian"
	if buildAllSources == "true" {
		slug = "zhenguojian"
	}
	if !strings.EqualFold(filepath.Base(executable), slug+".exe") {
		return errors.New("请从正式发布程序执行更新")
	}
	target := filepath.Dir(executable)
	job := rand.Text()
	directory := filepath.Join(e.directory, "updates", job)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	oldFiles, err := installedAppFiles(target)
	if err != nil {
		return err
	}
	plan := appInstallPlan{Target: target, Stage: filepath.Join(directory, "stage"), Backup: filepath.Join(directory, "backup"), Executable: slug + ".exe", PID: os.Getpid(), Token: job, Ready: filepath.Join(directory, "ready.json"), Result: filepath.Join(e.directory, "updates", "last-result.json"), History: filepath.Join(e.directory, "updates", "history.json"), Version: state.Version, PreviousVersion: nativeAppVersion + "+" + strconv.Itoa(nativeAppBuild), OldFiles: oldFiles, Timeout: 90}
	if rollback {
		var previous appInstallPlan
		raw, err := os.ReadFile(plan.History)
		if err != nil {
			return err
		}
		if json.Unmarshal(raw, &previous) != nil || filepath.Clean(previous.Target) != filepath.Clean(target) || !e.appRollbackAvailable() {
			return errors.New("没有可用的程序备份")
		}
		plan.Stage = previous.Backup
		plan.Files = previous.OldFiles
		plan.Version = previous.PreviousVersion
	} else {
		if err := verifyUpdateArchive(state.Path, state.SHA256); err != nil {
			return err
		}
		files, err := unpackAppUpdate(state.Path, plan.Stage, plan.Executable)
		if err != nil {
			return err
		}
		plan.Files = files
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	planFile := filepath.Join(directory, "install-plan.json")
	if err := writeNativeCacheFile(planFile, raw); err != nil {
		return err
	}
	helper := filepath.Join(directory, "update-helper.ps1")
	if err := os.WriteFile(helper, append([]byte{0xef, 0xbb, 0xbf}, appUpdateHelper...), 0600); err != nil {
		return err
	}
	command := exec.Command(filepath.Join(os.Getenv("WINDIR"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", helper, "-PlanPath", planFile)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000200 | 0x00000008}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
