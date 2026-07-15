package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAutoScopedCommandAllowsRoutineWorkspaceDevelopment(t *testing.T) {
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "internal", "queue")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	ag := New(nil, nil, 4096)
	ag.SetWorkDir(workspace)

	for _, command := range []string{
		"go test ./...",
		"cd " + workspace + " && go build ./... 2>&1",
		"cd internal/queue && go test ./...",
		"gofmt -w internal/queue/policy.go && go test ./internal/queue",
		"sed -n '1,120p' internal/queue/policy.go",
		"sed -n '$p' internal/queue/policy.go",
		"sed -n '1p' -- internal/queue/policy.go",
		"go test -run 'Test(Foo|Bar)' ./...",
		"rg 'value?' .",
		"rg '/api/v1' .",
		"grep '/healthz' README.md",
		"go test -run=/subtest ./...",
		"bun run build | head -20",
		"npm run site:build",
		"task site:verify",
		"CI=1 cargo check",
		"date",
	} {
		t.Run(command, func(t *testing.T) {
			if !ag.autoScopedCommandAllowed(command) {
				t.Fatalf("routine command was not admitted in AUTO: %q", command)
			}
		})
	}
}

func TestAutoScopedCommandGatesDynamicDestructiveAndExternalEffects(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	ag := New(nil, nil, 4096)
	ag.SetWorkDir(workspace)

	commands := []string{
		"rm -rf .",
		"git push origin main",
		"git status --short",
		"git diff --stat",
		"git add .",
		"git commit -m done",
		"git tag v0.1.0",
		"git switch feature",
		"git merge feature",
		"git rebase main",
		"git cherry-pick deadbeef",
		"git branch -D old-work",
		"git branch -Dold-work",
		"git tag --delete v0.1.0",
		"git tag -dv0.1.0",
		"git switch --discard-changes main",
		"git switch -fmain",
		"git rebase --exec 'curl https://example.test' main",
		"git rebase '-xcurl https://example.test' main",
		"git diff --ext-diff",
		"git diff --textconv",
		"git grep --open-files-in-pager=less needle",
		"git commit -F/etc/passwd",
		"git tag -F/etc/passwd v0.1.0",
		"make -f" + filepath.Join(outside, "Makefile") + " test",
		"make -ksf" + filepath.Join(outside, "Makefile") + " test",
		"sort input -o" + filepath.Join(outside, "sorted.txt"),
		"sort -bo" + filepath.Join(outside, "sorted-cluster.txt") + " input",
		"sort --files0-from=paths0",
		"sort --files0-fro=paths0",
		"sort --compress-prog=false /dev/null",
		"tree -o" + filepath.Join(outside, "tree.txt") + " .",
		"tree -do" + filepath.Join(outside, "tree-cluster.txt") + " .",
		"find -f" + filepath.Join(outside, "secret.txt"),
		"grep -Hf" + filepath.Join(outside, "patterns.txt") + " /dev/null",
		"grep /etc/hosts -e localhost",
		"rg /etc/hosts -e localhost",
		"grep -f patterns /etc/passwd",
		"rg -f patterns /etc/passwd",
		"grep '' /etc/passwd",
		`rg "" /etc/passwd`,
		"grep -e '' /etc/passwd",
		`rg -e "" /etc/passwd`,
		"rg --files /etc",
		"file -bf" + filepath.Join(outside, "files.txt") + " /dev/null",
		"file -f file-list.txt",
		"file --files-fr=file-list.txt",
		"file -m" + filepath.Join(outside, "magic") + " /dev/null",
		"file -m local:/etc/magic target",
		"file -M local:/etc/magic target",
		"file -bz /dev/null",
		"file -bZ /dev/null",
		"file --uncompress-n /dev/null",
		"file -C /dev/null",
		"file -S /dev/null",
		"file --no-sand /dev/null",
		"rg -zP needle /dev/null",
		"rg -Pz needle /dev/null",
		"touch -r" + filepath.Join(outside, "reference") + " target",
		"npm run deploy",
		"npm run site:deploy",
		"npm run site",
		"bun run site",
		"npm run build:watch",
		"npm run test:watch",
		"npm run site:dev",
		"npm run docs:serve",
		"npm run site:preview",
		"npm run test:ui",
		"npm run test:open",
		"npm run test:debug",
		"npm run lint:inspect",
		"npm run lint:mcp",
		"npm run format -- --plugin=file:///private/tmp/plugin.mjs",
		"npm run lint -- --inspect-config",
		"npm test --node-options=--inspect-wait",
		"npm test --node-op=--inspect-wait",
		"npm run BUILD",
		`npm run "build "`,
		"npm test -- --watch",
		"pnpm run test --watch=true",
		"yarn test --watchAll",
		"yarn run test --watch-all",
		"bun run build --watch",
		"bun build",
		"bun build --watch ./src/index.ts",
		"bun test --hot",
		"task release",
		"task site",
		"task docs:serve",
		"make test-watch",
		"task VERIFY",
		`make "test "`,
		"make clean",
		"make test --eval='x:; touch /private/tmp/pwn'",
		"make test -E 'x:; touch /private/tmp/pwn'",
		"make test CMD='touch /private/tmp/pwn'",
		"touch " + filepath.Join(outside, "pwn=foo"),
		"touch /dev/null",
		"touch -r /dev/null target",
		"mkdir /dev/null",
		"mkdir " + filepath.Join(outside, "dir=x"),
		"sort -o " + filepath.Join(outside, "out=x") + " input",
		"sort --output=../outside.txt input",
		"touch name=../outside.txt",
		"rg --pre=sh pattern script.sh",
		"rg --search-zip pattern archive.zip",
		"rg --hostname-bin=env --hyperlink-format=default needle .",
		"cargo test --config 'target.x86_64-unknown-linux-gnu.runner=\"/private/tmp/evil\"'",
		"cargo doc --open",
		"go build -ldflags='-linkmode=external -extld=/private/tmp/evil' ./...",
		"golangci-lint cache clean",
		"golangci-lint custom",
		"date -s 2030-01-01",
		"curl https://example.test",
		"go test ./... > result.txt",
		"go env -w GOPROXY=https://example.test",
		"go test -exec=curl ./...",
		"go test -fuzz=FuzzParser ./...",
		"go test -test.fuzz=FuzzParser ./...",
		"go test --test.fuzzworker ./...",
		"go build -toolexec=touch ./...",
		"go test $(cat packages.txt)",
		"sh -c 'go test ./...'",
		"./go test ./...",
		filepath.Join(outside, "go") + " test ./...",
		"cd " + outside + " && go test ./...",
		"go build -o " + filepath.Join(outside, "app") + " ./cmd/app",
		"find . -delete",
		"find -L .",
		"find -XL .",
		"find -EL .",
		"find . -follow",
		"find -files0-from starts.txt",
		"rg -L needle .",
		"rg -nL needle .",
		"rg -NL needle .",
		"rg -UL needle .",
		"tree -l .",
		"tree -dl .",
		"du -L .",
		"du --files0-from=paths.txt",
		"grep -R needle .",
		"grep -nR needle .",
		"grep -rS needle .",
		"grep -Sr needle .",
		"ls -RL .",
		"ls -TRL .",
		"ls -IL .",
		"ls -wL .",
		"wc --files0-from=paths.txt",
		"diff -r . snapshot",
		"diff dir1 dir2",
		"diff -ru . snapshot",
		"diff -l before after",
		"diff -ul before after",
		"eslint -o" + filepath.Join(outside, "report.txt") + " .",
		"eslint --inspect-config eslint.config.js",
		"eslint --init",
		"eslint --mcp",
		"prettier --plugin file:///tmp/plugin.mjs .",
		"prettier --plugin=data:text/javascript,export%20default%20{} .",
		"tail -f app.log",
		"tail -F app.log",
		"tail -nf app.log",
		"tail --follow=name app.log",
		"tail --retry app.log",
		"tsc --watch",
		"tsc -w",
		"tsc -bw",
		"tsc --build --clean",
		"tsc -b --clean",
		"tsc --typeRoots foo,/etc no-such-file.ts",
		"tsc --typeRoots=foo,/etc no-such-file.ts",
		"tsc --rootDirs foo,/etc no-such-file.ts",
		"tsc --rootDirs=foo,/etc no-such-file.ts",
		"tsc @/etc/passwd",
		"swift build --disable-sandbox",
		"swift test -Xcc @/tmp/args.rsp",
		"swift build -Xswiftc -load-plugin-executable",
		"swift build -Xlinker /tmp/linker.args",
		"swift build -Xcxx @/tmp/cxx.rsp",
		"sed -i '' s/old/new/ file.go",
		"sed -i.bak s/old/new/ file.go",
		"sed -n '1,20w leaked.txt' file.go",
		"sed -n 1p file.go -i",
		"sed -n 1p file.go -e 'w leaked.txt'",
		"go get example.test/module",
		"command rm -rf .",
		"LD_PRELOAD=/tmp/inject.dylib go test ./...",
		"cat *.go",
		"cd .\\\n. && touch unexpected.txt",
		"rg --fo\\\nllow needle .",
		"find . -fo\\\nllow -del\\\nete",
		"cat safe\runsafe",
		"cat pivot/../etc/passwd",
		"cd nested ; cat outside-link/passwd",
		"cd nested | cat outside-link/passwd",
		"cd nested || cat outside-link/passwd",
	}
	if runtime.GOOS != "windows" {
		if err := os.MkdirAll(filepath.Join(workspace, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(workspace, "outside-link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(workspace, "nested", "outside-link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(string(filepath.Separator), filepath.Join(workspace, "pivot")); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, "cd nested && cat outside-link/passwd")
		if err := os.Symlink(outside, filepath.Join(workspace, "outside2>&1")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(workspace, "name=value")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(workspace, `outside\link`)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(workspace, "outside-nbsp\u00a0")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(workspace, "outside-space ")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(workspace, "outside-newline\n")); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, "cat outside-link/secret.txt")
		commands = append(commands, "make -foutside-link/Makefile test")
		commands = append(commands, "cat 'outside2>&1/secret.txt'")
		commands = append(commands, "touch 'name=value/new.txt'")
		commands = append(commands, `cat "outside\link/secret.txt"`)
		commands = append(commands, "cat outside-nbsp\u00a0")
		commands = append(commands, `cat "outside-space "`)
		commands = append(commands, "cat 'outside-newline\n'")
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			if ag.autoScopedCommandAllowed(command) {
				t.Fatalf("risky command gained AUTO authority: %q", command)
			}
		})
	}
}

func TestSplitStaticShellCommandsRejectsAmbiguousSyntax(t *testing.T) {
	for _, command := range []string{"go test &", "go test &&", "'unterminated", "go test < input"} {
		if _, _, ok := splitStaticShellCommands(command); ok {
			t.Fatalf("ambiguous shell syntax accepted: %q", command)
		}
	}
}

func TestAutoCommandPolicyHelpersRejectDelegationAndPersistentModes(t *testing.T) {
	for _, args := range [][]string{
		{"test", "-fuzz=FuzzParser", "./..."},
		{"test", "-test.fuzz=FuzzParser", "./..."},
		{"test", "--test.fuzzworker", "./..."},
	} {
		if autoScopedGoCommandAllowed(args) {
			t.Fatalf("Go fuzz mode admitted: %#v", args)
		}
	}
	for _, args := range [][]string{
		{"build", "--disable-sandbox"},
		{"test", "-Xcc", "@/tmp/args.rsp"},
		{"build", "-Xlinker=/tmp/linker.args"},
	} {
		if autoScopedSwiftCommandAllowed(args) {
			t.Fatalf("Swift delegation admitted: %#v", args)
		}
	}
	for _, args := range [][]string{
		{"@/etc/passwd"},
		{"--watch"},
		{"-bw"},
		{"--build", "--clean"},
		{"--typeRoots=foo,/etc"},
	} {
		if autoScopedTSCCommandAllowed(args) {
			t.Fatalf("TypeScript delegated or persistent mode admitted: %#v", args)
		}
	}
	for _, target := range []string{
		"site", "site:dev", "docs:serve", "build:watch", "test:ui", "test:open", "test:debug", "lint:inspect", "lint:mcp", "VERIFY", "test ",
	} {
		if autoRoutineTargetAllowed(target) {
			t.Fatalf("persistent or ambiguous task target admitted: %q", target)
		}
	}
	if !autoRoutineTargetAllowed("site:build") || !autoRoutineTargetAllowed("test:unit") {
		t.Fatal("finite routine task target was rejected")
	}
	if autoScopedPackageCommandAllowed([]string{"run", "format", "--", "--plugin=file:///tmp/plugin.mjs"}, "test") {
		t.Fatal("package-script trailing arguments were admitted")
	}
	if !containsLongOptionPrefix([]string{"--files0-fro=paths"}, "--files0-from") ||
		!containsLongOptionPrefix([]string{"--uncompress-n"}, "--uncompress-noreport") {
		t.Fatal("dangerous GNU long-option abbreviation was not recognized")
	}
}

func TestSplitStaticShellCommandsPreservesQuotedEmptyArguments(t *testing.T) {
	commands, separators, ok := splitStaticShellCommands(`grep '' README.md && rg -e "" .`)
	if !ok {
		t.Fatal("static command with quoted-empty arguments was rejected")
	}
	if len(separators) != 1 || separators[0] != "&&" {
		t.Fatalf("separators = %#v, want [&&]", separators)
	}
	want := [][]string{{"grep", "", "README.md"}, {"rg", "-e", "", "."}}
	if len(commands) != len(want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
	for index := range want {
		if len(commands[index]) != len(want[index]) {
			t.Fatalf("commands = %#v, want %#v", commands, want)
		}
		for argument := range want[index] {
			if commands[index][argument] != want[index][argument] {
				t.Fatalf("commands = %#v, want %#v", commands, want)
			}
		}
	}
}

func TestAutoScopedCommandRejectsWorkspaceExecutableShadowing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("AUTO shell catalog requires a POSIX sh")
	}
	workspace := t.TempDir()
	shadow := filepath.Join(workspace, "go")
	if err := os.WriteFile(shadow, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", workspace+string(os.PathListSeparator)+os.Getenv("PATH"))
	ag := New(nil, nil, 4096)
	ag.SetWorkDir(workspace)
	if ag.autoScopedCommandAllowed("go version") {
		t.Fatal("workspace executable shadow gained AUTO authority through PATH")
	}
}
