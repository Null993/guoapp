package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAppUpdateWindowsHelperInstallsAndRollsBack(t *testing.T) {
	compiler := filepath.Join(os.Getenv("WINDIR"), "Microsoft.NET", "Framework64", "v4.0.30319", "csc.exe")
	if _, err := os.Stat(compiler); err != nil {
		t.Skip("Windows .NET C# fixture compiler unavailable")
	}
	root := t.TempDir()
	t.Cleanup(func() { time.Sleep(4 * time.Second) })
	code := `using System; using System.IO; using System.Threading; class Fixture { static void Main() { string value=File.ReadAllText(Path.Combine(AppDomain.CurrentDomain.BaseDirectory,"fixture-ready-path.txt")); if(value=="FAIL")return; if(value!="OLD")File.WriteAllText(value,"{}"); Thread.Sleep(3000); } }`
	source := filepath.Join(root, "Fixture.cs")
	binary := filepath.Join(root, "fixture.exe")
	os.WriteFile(source, []byte(code), 0600)
	if body, err := exec.Command(compiler, "/nologo", "/target:exe", "/out:"+binary, source).CombinedOutput(); err != nil {
		t.Fatalf("compile fixture: %s %v", body, err)
	}
	program, _ := os.ReadFile(binary)
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy startup", true: "failed startup restores old files"}[failure], func(t *testing.T) {
			dir := filepath.Join(root, map[bool]string{false: "success", true: "failure"}[failure])
			os.MkdirAll(dir, 0700)
			target := filepath.Join(dir, "installed")
			stage := filepath.Join(dir, "stage")
			backup := filepath.Join(dir, "backup")
			os.MkdirAll(target, 0700)
			os.MkdirAll(stage, 0700)
			ready := filepath.Join(dir, "ready.json")
			for name, body := range map[string][]byte{"fixture.exe": program, "fixture-ready-path.txt": []byte("OLD"), "old.dll": []byte("old version"), "personal.txt": []byte("keep user file")} {
				os.WriteFile(filepath.Join(target, name), body, 0600)
			}
			content := ready
			if failure {
				content = "FAIL"
			}
			for name, body := range map[string][]byte{"fixture.exe": program, "fixture-ready-path.txt": []byte(content), "new.dll": []byte("new version")} {
				os.WriteFile(filepath.Join(stage, name), body, 0600)
			}
			plan := appInstallPlan{Target: target, Stage: stage, Backup: backup, Executable: "fixture.exe", Ready: ready, Token: "fixture", Result: filepath.Join(dir, "result.json"), History: filepath.Join(dir, "history.json"), Timeout: 2, Files: []string{"fixture.exe", "fixture-ready-path.txt", "new.dll"}, OldFiles: []string{"fixture.exe", "fixture-ready-path.txt", "old.dll"}}
			data, _ := json.Marshal(plan)
			planPath := filepath.Join(dir, "plan.json")
			os.WriteFile(planPath, data, 0600)
			helper := filepath.Join(dir, "helper.ps1")
			os.WriteFile(helper, append([]byte{0xef, 0xbb, 0xbf}, appUpdateHelper...), 0600)
			output, err := exec.Command(filepath.Join(os.Getenv("WINDIR"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", helper, "-PlanPath", planPath).CombinedOutput()
			var result map[string]any
			body, _ := os.ReadFile(plan.Result)
			json.Unmarshal(body, &result)
			if failure {
				if err == nil || result["status"] != "failed" {
					t.Fatalf("failed startup accepted: %v %s %s", err, body, output)
				}
				old, _ := os.ReadFile(filepath.Join(target, "old.dll"))
				if string(old) != "old version" {
					t.Fatal("original files not restored")
				}
				if _, err := os.Stat(filepath.Join(target, "new.dll")); !os.IsNotExist(err) {
					t.Fatal("new files left after rollback")
				}
			} else {
				if err != nil || result["status"] != "installed" {
					t.Fatalf("healthy startup failed: %v %s %s", err, body, output)
				}
				if _, err := os.Stat(filepath.Join(target, "old.dll")); !os.IsNotExist(err) {
					t.Fatal("obsolete binary kept in install directory")
				}
				old, _ := os.ReadFile(filepath.Join(backup, "old.dll"))
				if string(old) != "old version" {
					t.Fatal("missing backup")
				}
			}
			personal, _ := os.ReadFile(filepath.Join(target, "personal.txt"))
			if string(personal) != "keep user file" {
				t.Fatal("user file changed")
			}
		})
	}
}
