# klog Configuration Guide

Comprehensive guide to configuring klog via environment variables, theme files, and kubeconfig.

## Quick Start

Most users need nothing beyond their existing kubeconfig setup. klog uses:
- Your current kubeconfig context
- Your current namespace in that context
- Default theme colors

Override with flags as needed:
```bash
klog tail -n other-namespace --context production-cluster
```

---

## Configuration Methods

### 1. Command-Line Flags (Highest Priority)

Override any configuration with flags. See [COMMANDS.md](COMMANDS.md) for complete flag reference.

```bash
klog tail -n shop -d checkout --context staging
```

### 2. Environment Variables

Set defaults that apply to all invocations:

| Variable | Purpose | Example |
|----------|---------|---------|
| `KLOG_KUBECTL` | Path to kubectl binary | `KLOG_KUBECTL=/opt/bin/kubectl` |
| `NO_COLOR` | Disable ANSI colors | `NO_COLOR=1` |
| `KLOG_PROFILES` | Path to the profiles file | `KLOG_PROFILES=~/work/profiles.json` |

**Examples:**

```bash
# Use kubectl from specific location
export KLOG_KUBECTL=/usr/local/bin/kubectl
klog tail -n prod -d api

# Disable colors in piped output
NO_COLOR=1 klog tail | tee output.log

# Disable colors permanently
export NO_COLOR=1
klog tail  # colors disabled for all future commands
```

### 3. kubeconfig Context & Namespace

klog inherits from your kubeconfig:
- **Current context** — set via `kubectl config use-context`
- **Default namespace** — set via `kubectl config set-context --current --namespace=<name>`

Override with flags:

```bash
# Show current context and namespace
kubectl config get-contexts
kubectl config current-context

# Set default context and namespace
kubectl config use-context production-cluster
kubectl config set-context --current --namespace=default

# Override in klog
klog tail --context staging -n other-namespace
```

### 4. Theme File (Pretty Output Colors)

Customize colors for `--format pretty` output.

#### Theme File Locations

klog looks for theme files in this order:

1. **Explicit flag** — `--theme /path/to/theme.json` (highest priority)
2. **Default location** — user config directory:
   - Linux: `~/.config/klog/theme.json`
   - macOS: `~/Library/Application Support/klog/theme.json`
3. **Built-in defaults** — if no file found, use built-in colors

#### Theme File Format

JSON with ANSI SGR (Select Graphic Rendition) color codes:

```json
{
  "timestamp": "2",
  "levels": {
    "trace": "2",
    "debug": "2",
    "info": "",
    "warn": "33",
    "error": "1;31",
    "fatal": "1;97;41"
  },
  "trace": {
    "frame": "2",
    "caused_by": "31",
    "omitted": "2"
  },
  "keys": "36",
  "labels": ["36", "32", "33", "35", "34", "31"]
}
```

**Field descriptions:**

| Field | Purpose |
|-------|---------|
| `timestamp` | Color of timestamp in log output |
| `levels` | Color for each log level (trace, debug, info, warn, error, fatal) |
| `trace.frame` | Color of stack trace frame lines |
| `trace.caused_by` | Color of "caused by" lines in stack traces |
| `trace.omitted` | Color of "N more frames..." truncation lines |
| `keys` | Color of JSON field names in flattened output |
| `labels` | Cycle of colors for pod/container labels (rotated through) |

**SGR Code Reference:**

| Code | Effect | Code | Effect |
|------|--------|------|--------|
| `""` | default (no formatting) | `2` | dim |
| `1` | bold | `31` | red |
| `32` | green | `33` | yellow |
| `36` | cyan | `90` | bright black |
| `97` | bright white | `41` | red background |

Combine codes with semicolons: `1;31` = bold red, `1;97;41` = bold bright white on red.

#### Built-In Default Theme

```json
{
  "timestamp": "2",
  "levels": {
    "trace": "2",
    "debug": "2",
    "info": "",
    "warn": "33",
    "error": "1;31",
    "fatal": "1;97;41"
  },
  "trace": {
    "frame": "2",
    "caused_by": "31",
    "omitted": "2"
  },
  "keys": "36",
  "labels": ["36", "32", "33", "35", "34", "31"]
}
```

#### Creating a Custom Theme

**Dark theme with reduced contrast:**
```json
{
  "timestamp": "90",
  "levels": {
    "trace": "90",
    "debug": "36",
    "info": "37",
    "warn": "33",
    "error": "31",
    "fatal": "91"
  },
  "trace": {
    "frame": "90",
    "caused_by": "31",
    "omitted": "90"
  },
  "keys": "34",
  "labels": ["34", "35", "36", "32", "33"]
}
```

**High contrast theme:**
```json
{
  "timestamp": "1;37",
  "levels": {
    "trace": "90",
    "debug": "36",
    "info": "37",
    "warn": "1;33",
    "error": "1;31",
    "fatal": "1;97;41"
  },
  "trace": {
    "frame": "90",
    "caused_by": "1;31",
    "omitted": "90"
  },
  "keys": "1;36",
  "labels": ["1;34", "1;35", "1;36", "1;32", "1;33"]
}
```

#### Setting Up a Theme File

Linux:
```bash
mkdir -p ~/.config/klog
cat > ~/.config/klog/theme.json << 'EOF'
{
  "timestamp": "2",
  "levels": {
    "trace": "2",
    "debug": "2",
    "info": "",
    "warn": "33",
    "error": "1;31",
    "fatal": "1;97;41"
  },
  "trace": {
    "frame": "2",
    "caused_by": "31",
    "omitted": "2"
  },
  "keys": "36",
  "labels": ["36", "32", "33", "35", "34", "31"]
}
EOF
```

macOS:
```bash
mkdir -p ~/Library/Application\ Support/klog
cat > ~/Library/Application\ Support/klog/theme.json << 'EOF'
{
  "timestamp": "2",
  "levels": {
    "trace": "2",
    "debug": "2",
    "info": "",
    "warn": "33",
    "error": "1;31",
    "fatal": "1;97;41"
  },
  "trace": {
    "frame": "2",
    "caused_by": "31",
    "omitted": "2"
  },
  "keys": "36",
  "labels": ["36", "32", "33", "35", "34", "31"]
}
EOF
```

#### Using a Theme File

Default location (auto-loaded):
```bash
# Theme at ~/.config/klog/theme.json (Linux) or
# ~/Library/Application Support/klog/theme.json (macOS)
klog tail -n prod -d api
```

Explicit location:
```bash
klog tail -n prod -d api --theme ~/.klog-themes/dark.json
```

Override with default:
```bash
# Ignore ~/.config/klog/theme.json and use built-in colors
# (Can't be done directly; delete the file or use NO_COLOR instead)
NO_COLOR=1 klog tail -n prod -d api
```

### 5. Profiles (Saved Flag Sets)

A profile is a named list of flags. Put them in `profiles.json` in the klog config directory (`~/.config/klog/profiles.json` on Linux, `~/Library/Application Support/klog/profiles.json` on macOS), or point `KLOG_PROFILES` at another file. Each profile is an array of argv strings; there is no shell parsing, so every flag and value is its own element:

```json
{
  "checkout-prod": ["-n", "shop", "-d", "checkout", "--level", "WARN"],
  "web-errors":    ["-n", "shop", "-l", "app=web", "--level", "ERROR", "--dedupe"]
}
```

Start a command with `@name` to use one:

```bash
klog tail  @checkout-prod
klog fetch @checkout-prod --since 2h --out errors.log
```

`@name` is replaced by the profile's flags before anything is parsed, and your own flags come after them. So a scalar flag you pass (`--level ERROR`) overrides the profile's value (the last one wins), and repeatable flags (`--field`) add to the profile's. Only a leading `@name` right after `tail` or `fetch` is expanded.

An unknown profile, an unreadable file or invalid JSON is a usage error (exit 2); the message for an unknown profile lists the available names. A missing file simply means there are no profiles.

---

## Configuration Priority

When a setting has multiple sources, priority is (highest to lowest):

1. **Command-line flags** — `--context`, `--theme`, `--tz`, etc. (these override flags that come from an `@profile`)
2. **Environment variables** — `KLOG_KUBECTL`, `NO_COLOR`
3. **Theme file** — `~/.config/klog/theme.json` or `~/Library/Application Support/klog/theme.json`
4. **kubeconfig** — current context and namespace
5. **Built-in defaults** — hardcoded fallbacks

**Example:**
```bash
# Uses: staging context (flag), custom theme (flag), built-in colors if theme missing
klog tail --context staging --theme ~/my-theme.json -n prod -d api
```

---

## Common Configuration Scenarios

### Use Non-Standard kubectl

```bash
# Temporarily
KLOG_KUBECTL=/opt/kubectl klog tail -n prod -d api

# Permanently
export KLOG_KUBECTL=/opt/kubectl
klog tail -n prod -d api
```

### Disable Colors (for piping/logging)

```bash
# Temporarily
NO_COLOR=1 klog tail -n prod -d api | tee output.log

# Permanently
export NO_COLOR=1
```

### Switch Between Multiple kubeconfigs

```bash
# Set kubeconfig for this command only
KUBECONFIG=/path/to/other-kubeconfig klog tail -n prod -d api

# Or use kubectl to switch context
kubectl config use-context other-cluster
klog tail -n prod -d api
```

### Set Default Namespace Without --context

```bash
# Set namespace for current context
kubectl config set-context --current --namespace=prod

# Future klog commands default to 'prod' namespace
klog tail -d api  # uses 'prod' namespace
```

### Create Team Theme File

Share a custom theme across a team:

```bash
# Create theme file
mkdir -p /opt/klog/themes
cat > /opt/klog/themes/team-dark.json << 'EOF'
{
  "timestamp": "2",
  "levels": {
    "trace": "2",
    "debug": "36",
    "info": "37",
    "warn": "33",
    "error": "1;31",
    "fatal": "1;97;41"
  },
  "trace": {
    "frame": "2",
    "caused_by": "31",
    "omitted": "2"
  },
  "keys": "36",
  "labels": ["36", "32", "33", "35", "34", "31"]
}
EOF

# Use in alias
alias klog='command klog --theme /opt/klog/themes/team-dark.json'

# Or export for all users
export KLOG_THEME=/opt/klog/themes/team-dark.json
```

(Note: `KLOG_THEME` env var is not currently supported; use alias or explicit flag)

---

## Troubleshooting Configuration

### klog can't find kubectl

**Error:** `hint: kubectl not found; install it, or set KLOG_KUBECTL to its path`

**Solution:**
```bash
# Find kubectl
which kubectl
/usr/local/bin/kubectl

# Set KLOG_KUBECTL
export KLOG_KUBECTL=/usr/local/bin/kubectl
klog tail -n prod -d api
```

### Theme file not being loaded

**Check locations:**

Linux:
```bash
ls -la ~/.config/klog/theme.json
```

macOS:
```bash
ls -la ~/Library/Application\ Support/klog/theme.json
```

**Solution:**
- Create directory if missing: `mkdir -p ~/.config/klog`
- Validate JSON: `jq . ~/.config/klog/theme.json`
- Use explicit flag: `klog tail --theme ~/my-theme.json`

### Wrong context or namespace

```bash
# Check current context
kubectl config current-context

# List all contexts
kubectl config get-contexts

# Switch context
kubectl config use-context production

# Check default namespace
kubectl config view --minify | grep namespace

# Set default namespace
kubectl config set-context --current --namespace=prod
```

### Colors not showing

Check `NO_COLOR` env var:
```bash
echo $NO_COLOR

# If set, unset it
unset NO_COLOR

# Or check if stdout is a terminal
# Colors only show on terminal, not in pipes
klog tail -n prod -d api | cat  # no colors (piped)
klog tail -n prod -d api        # colors (terminal)
```

---

## Environment Setup Examples

### Bash/Zsh ~/.bashrc or ~/.zshrc

```bash
# klog configuration
export KLOG_KUBECTL=/usr/local/bin/kubectl
export NO_COLOR=0  # Set to 1 to disable colors

# Alias for common cluster
alias klog-prod='klog --context production'
alias klog-staging='klog --context staging'
```

### Fish ~/.config/fish/config.fish

```fish
# klog configuration
set -gx KLOG_KUBECTL /usr/local/bin/kubectl
set -gx NO_COLOR 0

# Aliases
alias klog-prod 'klog --context production'
alias klog-staging 'klog --context staging'
```

### Docker/Container

```dockerfile
FROM alpine:latest
RUN apk add --no-cache kubectl
COPY theme.json /etc/klog/theme.json
ENV KLOG_KUBECTL=/usr/bin/kubectl
ENTRYPOINT ["klog", "--theme", "/etc/klog/theme.json"]
```

---

## Advanced: Shell Completion Setup

While klog doesn't ship with completion scripts, you can create aliases and functions:

### Bash function for common operations

```bash
# Add to ~/.bashrc
klog-errors() {
  local ns="${1:-.}"
  local deployment="${2:-.}"
  if [ "$ns" = "." ] || [ "$deployment" = "." ]; then
    echo "Usage: klog-errors <namespace> <deployment>"
    return 1
  fi
  klog tail -n "$ns" -d "$deployment" --level ERROR
}

# Usage: klog-errors prod api-service
```

### Zsh function with completion

```bash
# Add to ~/.zshrc
klog-errors() {
  local ns="${1:?namespace required}"
  local deployment="${2:?deployment required}"
  klog tail -n "$ns" -d "$deployment" --level ERROR
}

_klog_errors() {
  _arguments '1: :(prod staging)' '2: :(api checkout inventory)'
}

compdef _klog_errors klog-errors
```
