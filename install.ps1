# Install fwdctl on Windows from a GitHub release: picks the amd64 build and checks its checksum.
#
#   irm https://raw.githubusercontent.com/forwardnetworks/fwdctl/main/install.ps1 | iex
#
# With the gh CLI installed and logged in the script uses it; otherwise it downloads over plain HTTPS.
# $env:FORWARD_SKILLS_VERSION = "v0.1.2"   pin a release (default: the latest)
# $env:FORWARD_SKILLS_BIN = "C:\tools"     install directory (default: %LOCALAPPDATA%\Programs\fwdctl)
$ErrorActionPreference = "Stop"

$repo = if ($env:FORWARD_SKILLS_GH_REPO) { $env:FORWARD_SKILLS_GH_REPO } else { "forwardnetworks/fwdctl" }
$dir = if ($env:FORWARD_SKILLS_BIN) { $env:FORWARD_SKILLS_BIN } else { Join-Path $env:LOCALAPPDATA "Programs\fwdctl" }

$useGh = $false
if (Get-Command gh -ErrorAction SilentlyContinue) { gh auth status *> $null; $useGh = ($LASTEXITCODE -eq 0) }
if ([Environment]::Is64BitOperatingSystem -eq $false -or $env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
  Write-Warning "fwdctl is built for Windows on amd64; on ARM it runs under emulation where Windows provides it"
}

$version = $env:FORWARD_SKILLS_VERSION
if (-not $version) {
  if ($useGh) { $version = (gh release view --repo $repo --json tagName -q .tagName).Trim() }
  else {
    try { $version = (Invoke-WebRequest -UseBasicParsing -Uri "https://github.com/$repo/releases/latest" -MaximumRedirection 0 -ErrorAction SilentlyContinue).Headers.Location -replace '.*/', '' } catch { }
    if (-not $version) { $version = ([Uri](Invoke-WebRequest -UseBasicParsing -Uri "https://github.com/$repo/releases/latest").BaseResponse.ResponseUri).Segments[-1] }
  }
}
if (-not $version) { throw "could not find the latest release of $repo" }

$asset = "fwdctl_${version}_windows_amd64.zip"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("fwdctl-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "installing fwdctl $version (windows_amd64)"
  if ($useGh) {
    gh release download $version --repo $repo --dir $tmp --pattern $asset --pattern SHA256SUMS
    if ($LASTEXITCODE -ne 0) { throw "download failed" }
  } else {
    $base = "https://github.com/$repo/releases/download/$version"
    try {
      Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile (Join-Path $tmp $asset)
      Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile (Join-Path $tmp "SHA256SUMS")
    } catch { throw "download failed: $_" }
  }

  $line = Get-Content (Join-Path $tmp "SHA256SUMS") | Where-Object { $_.EndsWith(" $asset") } | Select-Object -First 1
  if (-not $line) { throw "$asset is not listed in SHA256SUMS" }
  $want = ($line -split "\s+")[0].ToLower()
  $got = (Get-FileHash (Join-Path $tmp $asset) -Algorithm SHA256).Hash.ToLower()
  if ($got -ne $want) { throw "checksum mismatch for ${asset}: got $got, want $want" }

  New-Item -ItemType Directory -Path $dir -Force | Out-Null
  Expand-Archive -Path (Join-Path $tmp $asset) -DestinationPath $tmp -Force
  Copy-Item (Join-Path $tmp "fwdctl.exe") (Join-Path $dir "fwdctl.exe") -Force
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

& (Join-Path $dir "fwdctl.exe") version

# A user with no user-level PATH has none to read back: treat that as empty, not as an error.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($null -eq $userPath) { $userPath = "" }
if (($userPath -split ";") -notcontains $dir) {
  [Environment]::SetEnvironmentVariable("Path", (($userPath.TrimEnd(";") + ";" + $dir).TrimStart(";")), "User")
  Write-Host "added $dir to your user PATH; open a new terminal to use fwdctl"
}
