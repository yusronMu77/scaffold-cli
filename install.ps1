#!/usr/bin/env pwsh
# Installs the latest (or a pinned) scaffold-cli release for Windows.
#
#   irm https://raw.githubusercontent.com/yusronMu77/scaffold-cli/main/install.ps1 | iex
#
# Override the version with $env:SCAFFOLD_CLI_VERSION = "v0.3.0", and the install directory with
# $env:SCAFFOLD_CLI_INSTALL_DIR (defaults to "$env:LOCALAPPDATA\scaffold-cli\bin").
param(
  [string]$Version = $env:SCAFFOLD_CLI_VERSION,
  [string]$InstallDir = $env:SCAFFOLD_CLI_INSTALL_DIR
)

$ErrorActionPreference = "Stop"

$repo = "yusronMu77/scaffold-cli"
if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA "scaffold-cli\bin" }

switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { $arch = "amd64" }
  "ARM64" { $arch = "arm64" }
  default {
    Write-Error "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE"
    exit 1
  }
}

if (-not $Version) {
  $release = Invoke-RestMethod -UseBasicParsing "https://api.github.com/repos/$repo/releases/latest"
  $Version = $release.tag_name
  if (-not $Version) {
    Write-Error "Could not determine the latest release; pass -Version explicitly"
    exit 1
  }
}

$archive = "scaffold-cli_${Version}_windows_${arch}.zip"
$baseUrl = "https://github.com/$repo/releases/download/$Version"

$workDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $workDir | Out-Null
try {
  Write-Host "Downloading scaffold-cli $Version for windows/$arch..."
  Invoke-WebRequest -UseBasicParsing "$baseUrl/$archive" -OutFile (Join-Path $workDir $archive)
  Invoke-WebRequest -UseBasicParsing "$baseUrl/checksums.txt" -OutFile (Join-Path $workDir "checksums.txt")

  Write-Host "Verifying checksum..."
  $checksumLine = Select-String -Path (Join-Path $workDir "checksums.txt") -Pattern "  $archive$"
  if (-not $checksumLine) {
    Write-Error "$archive not listed in checksums.txt"
    exit 1
  }
  $expected = ($checksumLine.Line -split "\s+")[0]
  $actual = (Get-FileHash (Join-Path $workDir $archive) -Algorithm SHA256).Hash.ToLower()
  if ($expected -ne $actual) {
    Write-Error "Checksum mismatch for $archive"
    exit 1
  }

  Expand-Archive -Path (Join-Path $workDir $archive) -DestinationPath $workDir -Force

  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  Copy-Item (Join-Path $workDir "scaffold.exe") (Join-Path $InstallDir "scaffold.exe") -Force

  Write-Host "Installed scaffold-cli $Version to $InstallDir\scaffold.exe"

  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if (";$userPath;" -notlike "*;$InstallDir;*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$InstallDir", "User")
    Write-Host "Added $InstallDir to your User PATH. Restart your terminal for it to take effect."
  }

  & (Join-Path $InstallDir "scaffold.exe") --version
}
finally {
  Remove-Item -Recurse -Force $workDir -ErrorAction SilentlyContinue
}
