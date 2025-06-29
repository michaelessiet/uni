package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/fatih/color"
)

type NPMRegistrySearchResult struct {
	Objects []struct {
		Package struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Version     string `json:"version"`
			Links       struct {
				Homepage string `json:"homepage"`
			} `json:"links"`
			Author struct {
				Name string `json:"name"`
			} `json:"author"`
		} `json:"package"`
	} `json:"objects"`
}

type BrewCliInfoResponse struct {
	Formulae []struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Desc     string `json:"desc"`
		License  string `json:"license"`
		Homepage string `json:"homepage"`
	} `json:"formulae"`
	Casks []struct {
		Token    string `json:"token"`
		FullName string `json:"full_name"`
		Desc     string `json:"desc"`
		Homepage string `json:"homepage"`
	} `json:"casks"`
}

type CocoaPodsAPISearchResult struct {
	Results []struct {
		ID      string `json:"id"`
		Summary string `json:"summary"`
		Source  struct {
			Git string `json:"git"`
		} `json:"source"`
		Version string `json:"version"`
	} `json:"results"`
	Total int `json:"total"`
}

type PackageManagerInfo struct {
	Name                  string
	Executable            string
	LockFiles             []string
	InitArgs              []string
	InstallCmd            string
	InstallCmdWithoutArgs string // For commands like `npm install` without additional args
	ExecutionCmd          string // For commands like `npx <command>` or `bunx <command>`
	UninstallCmd          string
	SearchAPISupport      bool
	InstallationHint      string
}

var supportedManagers = map[string]PackageManagerInfo{
	// Node
	"npm":  {Name: "NPM", Executable: "npm", LockFiles: []string{"package-lock.json"}, InitArgs: []string{"init", "-y"}, InstallCmd: "install", InstallCmdWithoutArgs: "install", ExecutionCmd: "npx", UninstallCmd: "uninstall", SearchAPISupport: true, InstallationHint: "Install Node.js and npm from https://nodejs.org/"},
	"pnpm": {Name: "PNPM", Executable: "pnpm", LockFiles: []string{"pnpm-lock.yaml"}, InitArgs: []string{"init"}, InstallCmd: "add", InstallCmdWithoutArgs: "install", ExecutionCmd: "dlx", UninstallCmd: "remove", SearchAPISupport: true, InstallationHint: "Run: npm install -g pnpm"},
	"yarn": {Name: "Yarn", Executable: "yarn", LockFiles: []string{"yarn.lock"}, InitArgs: []string{"init", "-y"}, InstallCmd: "add", InstallCmdWithoutArgs: "install", ExecutionCmd: "dlx", UninstallCmd: "remove", SearchAPISupport: true, InstallationHint: "Run: npm install -g yarn"},
	"bun":  {Name: "Bun", Executable: "bun", LockFiles: []string{"bun.lockb", "bun.lock"}, InitArgs: []string{"init", "-y"}, InstallCmd: "add", InstallCmdWithoutArgs: "install", ExecutionCmd: "bunx", UninstallCmd: "remove", SearchAPISupport: true, InstallationHint: "Run: curl -fsSL https://bun.sh/install | bash"},
	// Cocoapods
	"pod": {Name: "CocoaPods", Executable: "pod", LockFiles: []string{"Podfile.lock"}, InitArgs: []string{"init"}, InstallCmd: "install", InstallCmdWithoutArgs: "", UninstallCmd: "", SearchAPISupport: true, InstallationHint: "Run: sudo gem install cocoapods"},
	// System Package Managers
	"brew": {Name: "Homebrew", Executable: "brew", LockFiles: []string{}, InitArgs: nil, InstallCmd: "install", InstallCmdWithoutArgs: "", UninstallCmd: "uninstall", SearchAPISupport: true, InstallationHint: "Install Homebrew from https://brew.sh/"},
	"pkgx": {Name: "pkgx", Executable: "pkgx", LockFiles: []string{"pkgx.yaml"}, InitArgs: nil, InstallCmd: "install", InstallCmdWithoutArgs: "", ExecutionCmd: "pkgx", UninstallCmd: "uninstall", SearchAPISupport: false, InstallationHint: "Run: curl -fsS https://pkgx.sh | sh"},
	// Python
	"pip":  {Name: "Pip", Executable: "pip", LockFiles: []string{"requirements.txt"}, InitArgs: nil, InstallCmd: "install", InstallCmdWithoutArgs: "", UninstallCmd: "uninstall", SearchAPISupport: false, InstallationHint: "Install Python and pip from https://www.python.org/"},
	"pipx": {Name: "Pipx", Executable: "pipx", LockFiles: []string{"pipx.json"}, InitArgs: nil, InstallCmd: "install", InstallCmdWithoutArgs: "", UninstallCmd: "uninstall", SearchAPISupport: false, InstallationHint: "Run: pip install --user pipx && python -m pipx ensurepath"},
	"uv":   {Name: "uv", Executable: "uv", LockFiles: []string{"uv.lock", "pylock.toml"}, InitArgs: []string{"init"}, InstallCmd: "add", InstallCmdWithoutArgs: "", UninstallCmd: "remove", SearchAPISupport: false, InstallationHint: "Install uv from https://docs.astral.sh/uv"},
	// Go
	"go": {Name: "Go", Executable: "go", LockFiles: []string{"go.mod"}, InitArgs: nil, InstallCmd: "get", InstallCmdWithoutArgs: "", UninstallCmd: "get -u", SearchAPISupport: false, InstallationHint: "Install Go from https://golang.org/dl/"},
}

const uniConfigFile = ".unirc"

var httpClient = &http.Client{Timeout: 10 * time.Second}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printHelp()
		return
	}
	var specifiedManager string
	if len(args) > 0 && strings.HasPrefix(args[0], "--pkg=") {
		specifiedManager = strings.TrimPrefix(args[0], "--pkg=")
		args = args[1:]
	}
	if len(args) > 0 {
		command := args[0]
		commandArgs := args[1:]
		switch command {
		case "init":
			if len(commandArgs) != 1 {
				color.Red("Usage: uni init <package_manager>")
				os.Exit(1)
			}
			handleInit(commandArgs[0])
			return
		case "search", "s":
			if len(commandArgs) == 0 {
				color.Red("Usage: uni search <query>")
				os.Exit(1)
			}
			manager, _ := detectPackageManager(specifiedManager)
			handleApiSearch(manager, strings.Join(commandArgs, " "))
			return
		case "x", "exec":
			if len(commandArgs) == 0 {
				color.Red("Usage: uni x <command> [args...]")
				os.Exit(1)
			}
			manager, _ := detectPackageManager(specifiedManager)
			color.Cyan("▶️  Executing command: %s %s", manager.ExecutionCmd, strings.Join(commandArgs, " "))
			var cmd *exec.Cmd
			switch manager.Name {
			case "PNPM", "Yarn":
				cmd = exec.Command(manager.Executable, fmt.Sprintf("%s %s", manager.ExecutionCmd, strings.Join(commandArgs, " ")))
			default:
				cmd = exec.Command(manager.ExecutionCmd, commandArgs...)
			}
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			if err := cmd.Run(); err != nil {
				color.Red("Error executing command: %v", err)
				os.Exit(1)
			}
			return
		}
	}
	manager, err := detectPackageManager(specifiedManager)
	if err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
	color.Cyan("▶️  Using %s...", manager.Name)
	executeCliCommand(manager, args)
}

func handleApiSearch(pm PackageManagerInfo, query string) {
	if !pm.SearchAPISupport {
		color.Yellow("%s does not support API search. Falling back to CLI.", pm.Name)
		executeCliCommand(pm, []string{"search", query})
		return
	}

	color.Cyan("🔍 Searching for '%s' using %s...", query, pm.Name)

	var err error
	switch pm.Name {
	case "NPM", "PNPM", "Yarn", "Bun":
		err = searchNPM(query)
	case "Homebrew":
		// New: Use the local CLI JSON method for Homebrew
		err = searchHomebrewCliJson(query)
	case "CocoaPods":
		err = searchCocoaPods(query)
	default:
		color.Red("API search not implemented for %s.", pm.Name)
	}

	if err != nil {
		color.Red("Search failed: %v", err)
	}
}

func searchHomebrewCliJson(query string) error {
	searchCmd := exec.Command("brew", "search", query)
	var searchOut bytes.Buffer
	searchCmd.Stdout = &searchOut
	if err := searchCmd.Run(); err != nil {
		color.Yellow("No results found for '%s' in Homebrew search.", query)
	}

	scanner := bufio.NewScanner(&searchOut)
	var resultsFound bool
	for scanner.Scan() {
		line := scanner.Text()
		// `brew search` can have headers or empty lines, we ignore them.
		if strings.HasPrefix(line, "==>") || line == "" {
			continue
		}
		pkgName := strings.Fields(line)[0] // Get the first word of the line

		infoCmd := exec.Command("brew", "info", "--json=v2", pkgName)
		var infoOut bytes.Buffer
		infoCmd.Stdout = &infoOut
		if err := infoCmd.Run(); err != nil {
			continue
		}

		var results BrewCliInfoResponse
		if err := json.Unmarshal(infoOut.Bytes(), &results); err != nil {
			continue // Skip if JSON is unparsable
		}

		for _, item := range results.Formulae {
			resultsFound = true
			printPackageInfo(map[string]string{
				"Name":        item.Name,
				"Description": item.Desc,
				"License":     item.License,
				"Type":        "Formula",
				"Homepage":    item.Homepage,
			})
		}
		for _, item := range results.Casks {
			resultsFound = true
			printPackageInfo(map[string]string{
				"Name":        item.Token,
				"Description": item.Desc,
				"Type":        "Cask",
				"Homepage":    item.Homepage,
			})
		}
	}

	if !resultsFound {
		color.Yellow("No formulae or casks found.")
	}

	return nil
}

func searchNPM(query string) error {
	resp, err := httpClient.Get("https://registry.npmjs.org/-/v1/search?text=" + url.QueryEscape(query) + "&size=10")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var results NPMRegistrySearchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return fmt.Errorf("could not parse NPM response: %w", err)
	}
	if len(results.Objects) == 0 {
		color.Yellow("No packages found.")
		return nil
	}
	for _, item := range results.Objects {
		pkg := item.Package
		printPackageInfo(map[string]string{
			"Name":        pkg.Name,
			"Description": pkg.Description,
			"Version":     pkg.Version,
			"Homepage":    pkg.Links.Homepage,
			"Author":      pkg.Author.Name,
		})
	}
	return nil
}

func searchCocoaPods(query string) error {
	resp, err := httpClient.Get("https://search.cocoapods.org/api/v1/pods.flat.hash.json?query=" + url.QueryEscape(query) + "&amount=10")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var results CocoaPodsAPISearchResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return fmt.Errorf("could not parse CocoaPods response: %w", err)
	}
	if results.Total == 0 {
		color.Yellow("No pods found.")
		return nil
	}
	for _, item := range results.Results {
		printPackageInfo(map[string]string{
			"Name":        item.ID,
			"Description": item.Summary,
			"Version":     item.Version,
			"Source":      item.Source.Git,
		})
	}
	return nil
}

func printPackageInfo(info map[string]string) {
	fmt.Println(color.YellowString("---"))
	keyColor := color.New(color.FgGreen)
	for key, val := range info {
		if val != "" {
			keyColor.Printf("%-14s", key+":")
			fmt.Printf("%s\n", val)
		}
	}
}

func executeCliCommand(pm PackageManagerInfo, args []string) {
	if _, err := exec.LookPath(pm.Executable); err != nil {
		color.Red("Error: %s (%s) is not installed or not in your PATH.", pm.Name, pm.Executable)
		color.Yellow("Hint: %s", pm.InstallationHint)
		os.Exit(1)
	}
	if len(args) > 0 {
		switch args[0] {
		case "install", "i", "add":
			if len(args) == 1 && pm.InstallCmdWithoutArgs != "" {
				args[0] = pm.InstallCmdWithoutArgs
			} else if pm.InstallCmd == "" {
				color.Red("%s does not have a standard install command.", pm.Name)
				os.Exit(1)
			} else {
				args[0] = pm.InstallCmd
			}
		case "uninstall", "remove", "rm", "un":
			if pm.UninstallCmd == "" {
				color.Red("%s does not have a standard uninstall command.", pm.Name)
				os.Exit(1)
			}
			args[0] = pm.UninstallCmd
		}
	}
	cmd := exec.Command(pm.Executable, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	color.HiBlack("+ %s %s", pm.Executable, strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}

func detectPackageManager(specifiedManager string) (PackageManagerInfo, error) {
	if specifiedManager != "" {
		if pm, ok := supportedManagers[specifiedManager]; ok {
			return pm, nil
		}
		return PackageManagerInfo{}, fmt.Errorf("specified package manager '%s' is not supported", specifiedManager)
	}
	if config, err := os.ReadFile(uniConfigFile); err == nil {
		managerKey := strings.TrimSpace(string(config))
		if pm, ok := supportedManagers[managerKey]; ok {
			color.Yellow("Found '%s' config file, using %s.", uniConfigFile, pm.Name)
			return pm, nil
		}
	}
	for key, pm := range supportedManagers {
		if len(pm.LockFiles) > 0 {
			for _, lockFile := range pm.LockFiles {
				if _, err := os.Stat(lockFile); err == nil {
					color.Yellow("Found '%s' lock file, using %s.", lockFile, pm.Name)
					return pm, nil
				}
			}
		}
		if key == "pod" {
			if _, err := os.Stat("Podfile"); err == nil {
				return supportedManagers[key], nil
			}
		}
	}
	color.Yellow("No project file detected, falling back to system package manager.")
	if _, err := exec.LookPath("brew"); err == nil {
		return supportedManagers["brew"], nil
	}
	return supportedManagers["pkgx"], nil
}

func handleInit(managerKey string) {
	pm, ok := supportedManagers[managerKey]
	if !ok {
		color.Red("Error: Package manager '%s' is not supported for init.", managerKey)
		os.Exit(1)
	}
	color.Green("Initializing new %s project...", pm.Name)
	err := os.WriteFile(uniConfigFile, []byte(managerKey), 0644)
	if err != nil {
		color.Red("Failed to write %s file: %v", uniConfigFile, err)
		os.Exit(1)
	}
	color.Green("Created '%s' to use %s in this directory.", uniConfigFile, pm.Name)
	if pm.InitArgs != nil {
		color.Cyan("Running '%s %s'...", pm.Executable, strings.Join(pm.InitArgs, " "))
		executeCliCommand(pm, pm.InitArgs)
	}
}

func printHelp() {
	fmt.Println(color.CyanString("uni - The Universal Package Manager Wrapper"))
	fmt.Println("\n" + color.YellowString("Usage:"))
	fmt.Println("  uni <command> [args...]")
	fmt.Println("  uni init <manager>")
	fmt.Println("  uni --pkg=<manager> <command> [args...]")
	fmt.Println("\n" + color.YellowString("Commands:"))
	fmt.Println("  install, add, i        Install packages")
	fmt.Println("  uninstall, rm, un      Remove packages")
	fmt.Println("  search, s              Search for packages using official APIs or local commands")
	fmt.Println("  init                   Initialize a new project with a specific manager")
	fmt.Println("  run, ...               Any other command is passed through (e.g., 'uni run dev')")
	fmt.Println("\n" + color.YellowString("Examples:"))
	fmt.Println(color.GreenString("  uni install fastify      ") + "# Automatically uses npm/pnpm/yarn/bun")
	fmt.Println(color.GreenString("  uni search react         ") + "# Search for 'react' using the detected manager's API")
	fmt.Println(color.GreenString("  uni --pkg=brew s git     ") + "# Search for 'git' using Homebrew's local command")
}
