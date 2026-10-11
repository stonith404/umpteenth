//go:build unit

package playbook_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stonith404/umpteenth/backend/internal/playbook"
)

func TestFindInstall(t *testing.T) {
	installs := map[string]string{
		"pip with a flag":              "pip install --user matplotlib",
		"pip through subprocess":       `subprocess.run([sys.executable, "-m", "pip", "install", "requests"], check=True)`,
		"pip with a version":           "python3 -m pip install -q 'requests>=2' > /dev/null",
		"pip from a URL":               "pip install git+https://github.com/owner/repo",
		"uv pip":                       "uv pip install --system pandas",
		"uv run --with":                "uv run --with pandas python3 x.py",
		"uv run --with=":               "uv run --with=pandas python3 x.py",
		"pip with extras":              "pip install 'requests[socks]'",
		"uv run --with in a shebang":   "#!/usr/bin/env -S uv run --with requests python3\nprint(1)",
		"uv python":                    "uv python install 3.14",
		"global npm":                   "npm i --global pnpm",
		"system packages":              "sudo apt-get install -y ffmpeg",
		"system packages after a flag": "apt-get -y install ffmpeg",
		"uvx":                          "uvx ruff check .",
		"uvx on a later line":          "set -e\nuvx ruff check .",
		"npx -y":                       "npx -y prettier --check .",
		"go install of a module":       "go install golang.org/x/tools/cmd/goimports@latest",
		"curl into a shell":            "curl -fsSL https://example.com/install.sh | sh",
		"version manager":              "nvm install 26",
		"runtime download":             "import urllib.request\nv = 'v26.0.0'\nurl = f\"https://nodejs.org/dist/{v}/node-{v}-linux-x64.tar.xz\"\nurllib.request.urlretrieve(url, '/tmp/node.tar.xz')",
		"release archive on PATH":      "curl -sLO https://github.com/cli/cli/releases/download/v2.80.0/gh_2.80.0_linux_amd64.tar.gz && tar xzf gh_2.80.0_linux_amd64.tar.gz\nexport PATH=$PWD/gh_2.80.0_linux_amd64/bin:$PATH",
		"inline dependencies":          "# /// script\n# dependencies = [\"requests\"]\n# ///\nimport requests",
	}
	for name, script := range installs {
		t.Run(name, func(t *testing.T) {
			assert.NotEmpty(t, playbook.FindInstall(script))
		})
	}

	nothing := map[string]string{
		"requirements file":                 "pip install -r requirements.txt",
		"requirements file into a log":      "pip install -r requirements.txt > pip.log",
		"editable project":                  "pip install -e .",
		"editable project with extras":      `pip install -e ".[dev]"`,
		"unquoted extras":                   "pip install -e .[dev]",
		"quiet editable extras":             "pip install -q -e '.[dev]'",
		"project with extras":               `pip install ".[test]"`,
		"uv run with requirements":          "uv run --with-requirements requirements.txt pytest",
		"uv run with an editable project":   "uv run --with-editable . pytest",
		"requirements file from subprocess": `subprocess.run(["pip", "install", "-r", "req.txt"], check=True)`,
		"npm ci":                            "cd repo && npm ci && npm run build",
		"npm install in a project":          "cd repo && npm install",
		"local npm package":                 "npm install puppeteer",
		"install in a comment":              "# Needs: pip install requests\nimport requests",
		"install in a JavaScript comment":   "// run npm install -g pnpm first\nconsole.log(1)",
		"go install of a local command":     "go install ./cmd/tool",
		"runtime index":                     "curl -s https://nodejs.org/dist/index.json | jq '.[0].version'",
		"local archive and the toolkit":     "zip -r out.zip x; export PATH=/ump/toolkit:$PATH",
		"inline metadata without deps":      "# /// script\n# requires-python = \">=3.11\"\n# ///\nprint(1)",
		"utility model":                     "ump llm --system 'Write context' - < item.txt",
		"plain commands":                    "mkdir -p /workspace/out\ngh pr list --json number --jq '.[]'",
	}
	for name, script := range nothing {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, playbook.FindInstall(script))
		})
	}

	// The result is the line that installs, so a rejection can point at it
	assert.Equal(t, `url = f"https://nodejs.org/dist/{v}/node-{v}-linux-x64.tar.xz"`, playbook.FindInstall(installs["runtime download"]))
	assert.Equal(t, "uvx ruff check .", playbook.FindInstall(installs["uvx on a later line"]))
	assert.Equal(t, `# dependencies = ["requests"]`, playbook.FindInstall(installs["inline dependencies"]))
	assert.Equal(t, "pip install --user matplotlib", playbook.FindInstall("import os\n\n    pip   install  --user matplotlib  \nprint(1)"))
}
