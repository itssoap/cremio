<#
.SYNOPSIS
    Cremio Windows installer and updater
.DESCRIPTION
    Downloads the latest cremio binary from GitHub Releases, installs it
    to %LOCALAPPDATA%\cremio\bin\, and adds it to your user PATH.
    Running the script again checks for updates and upgrades if a newer
    version is available.
.PARAMETER InstallDir
    Directory to install cremio into. Defaults to $env:LOCALAPPDATA\cremio\bin.
.PARAMETER NoPath
    Skip adding the install directory to PATH.
.EXAMPLE
    irm https://raw.githubusercontent.com/itssoap/cremio/main/install.ps1 | iex
.NOTES
    One-liner: irm https://raw.githubusercontent.com/itssoap/cremio/main/install.ps1 | iex
#>

function Install-Cremio {
    [CmdletBinding()]
    param(
        [string]$InstallDir = "$env:LOCALAPPDATA\cremio\bin",
        [switch]$NoPath
    )

    $ErrorActionPreference = "Stop"
    $ProgressPreference    = "SilentlyContinue"

    $Repo       = "itssoap/cremio"
    $Binary     = "cremio.exe"
    $VersionFile = Join-Path (Split-Path $InstallDir -Parent) ".version"

    # --- helpers ---------------------------------------------------------
    function Write-Step($msg) {
        Write-Host ":: " -NoNewline -ForegroundColor Cyan
        Write-Host $msg
    }

    function Write-Info($msg) {
        Write-Host "   $msg" -ForegroundColor Gray
    }

    function Get-ArchSuffix {
        $arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture) {
            "Arm64" { "arm64" }
            "X86"   { "386"   }
            default { "amd64" }
        }
        return "windows-$arch"
    }

    function Get-LatestRelease {
        $api = "https://api.github.com/repos/$Repo/releases/latest"
        for ($attempt = 1; $attempt -le 2; $attempt++) {
            try {
                return Invoke-RestMethod -Uri $api -Method Get -ErrorAction Stop
            } catch {
                $status = $null
                try { $status = [int]$_.Exception.Response.StatusCode } catch {}

                if ($status -in @(403, 429)) {
                    $resetEpoch = $null
                    try { $resetEpoch = $_.Exception.Response.Headers.GetValues('X-RateLimit-Reset')[0] } catch {}
                    if ($resetEpoch) {
                        $resetAt = [DateTimeOffset]::FromUnixTimeSeconds([long]$resetEpoch).LocalDateTime.ToString('HH:mm')
                        throw "GitHub API rate limit exceeded. Limit resets at $resetAt -- try again then, OR fetch it from https://github.com/itssoap/cremio/releases/latest."
                    }
                    throw "GitHub API rate limit exceeded. Wait a few minutes and try again OR fetch it from https://github.com/itssoap/cremio/releases/latest."
                }

                if ($attempt -lt 2) {
                    Write-Info "GitHub API call failed, retrying..."
                } else {
                    throw "Could not reach GitHub API. Check your internet connection and try again.`n$($_.Exception.Message)"
                }
            }
        }
    }

    # --- main ------------------------------------------------------------
    Write-Host ""
    Write-Host "  Cremio Windows Installer" -ForegroundColor Green
    Write-Info "https://github.com/$Repo"
    Write-Host ""

    # 1. Fetch latest release info
    Write-Step "Fetching latest release..."
    $release = Get-LatestRelease
    $tag     = $release.tag_name
    Write-Info "Latest release: $tag"

    # 2. Detect existing installation (PATH or managed install dir) and version
    $existingBin     = $null
    $existingVersion = $null

    $existingCmd = Get-Command $Binary -ErrorAction SilentlyContinue
    if ($existingCmd) {
        $existingBin = $existingCmd.Source
        # Read version from Windows resource info (embedded by go-winres)
        try {
            $vi = (Get-Item $existingBin -ErrorAction Stop).VersionInfo
            if ($vi.ProductVersion -and $vi.ProductVersion -notmatch '^0\.0') {
                $existingVersion = $vi.ProductVersion.TrimStart('v')
            }
        } catch {}
    }

    # Fall back to the installer-managed .version file
    if (-not $existingVersion -and (Test-Path $VersionFile)) {
        $existingVersion = (Get-Content $VersionFile -Raw).Trim()
    }

    # Compare versions (strip leading 'v' for normalisation)
    $normTag      = $tag.TrimStart('v')
    $normExisting = if ($existingVersion) { $existingVersion.TrimStart('v') } else { $null }

    if ($normExisting -and $normExisting -eq $normTag) {
        $displayPath = if ($existingBin) { $existingBin } else { Join-Path $InstallDir $Binary }
        Write-Host ""
        Write-Host "  Cremio v$normExisting is already up to date at:" -ForegroundColor DarkGreen
        Write-Info $displayPath
        Write-Host ""
        return
    }

    if ($existingVersion) {
        Write-Step "Updating cremio v$($existingVersion.TrimStart('v')) -> $tag"
        if ($existingBin) { Write-Info "Found existing binary: $existingBin" }
    } else {
        Write-Step "Installing cremio $tag..."
    }

    # 3. Pick the right asset for this architecture
    $archSuffix  = Get-ArchSuffix
    $assetPattern = "*$archSuffix.exe"
    $asset = $release.assets | Where-Object { $_.name -like $assetPattern } | Select-Object -First 1

    # ARM64 Windows can run amd64 binaries via emulation — fall back if no native asset
    if (-not $asset -and $archSuffix -eq "windows-arm64") {
        Write-Info "No native arm64 asset found, falling back to amd64 (runs via x64 emulation)"
        $archSuffix   = "windows-amd64"
        $assetPattern = "*$archSuffix.exe"
        $asset = $release.assets | Where-Object { $_.name -like $assetPattern } | Select-Object -First 1
    }

    if (-not $asset) {
        throw "No release asset found matching '$archSuffix'. Available assets: $($release.assets.name -join ', ')"
    }

    Write-Info "Downloading $($asset.name) ..."

    # 4. Download and install
    # Replace the existing binary in-place when writable; otherwise use $InstallDir
    $dest            = Join-Path $InstallDir $Binary
    $replacedInPlace = $false
    if ($existingBin -and $existingBin -ne $dest) {
        $testFile = Join-Path (Split-Path $existingBin -Parent) ".cremio_write_test_$(Get-Random)"
        try {
            [System.IO.File]::WriteAllText($testFile, "")
            Remove-Item $testFile -Force -ErrorAction SilentlyContinue
            $dest            = $existingBin
            $replacedInPlace = $true
            Write-Info "Replacing in-place: $dest"
        } catch {
            Write-Info "Cannot write to $(Split-Path $existingBin -Parent) -- installing to $InstallDir instead"
        }
    }

    if (-not (Test-Path (Split-Path $dest -Parent))) {
        New-Item -ItemType Directory -Path (Split-Path $dest -Parent) -Force | Out-Null
    }

    # Download to temp file, then move (robust against partial downloads)
    $tmpFile = Join-Path $env:TEMP "cremio_$([Guid]::NewGuid().ToString('N')).exe"
    try {
        Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $tmpFile -ErrorAction Stop
        Move-Item -Path $tmpFile -Destination $dest -Force -ErrorAction Stop
    } finally {
        if (Test-Path $tmpFile) { Remove-Item $tmpFile -Force -ErrorAction SilentlyContinue }
    }

    Write-Info "Installed to $dest"

    # 5. Record version
    $tag | Set-Content -Path $VersionFile -Force -NoNewline

    # 6. Add to PATH (skip if we replaced an existing binary that is already on PATH)
    if (-not $NoPath -and -not $replacedInPlace) {
        Write-Step "Checking PATH..."

        $currentUserPath = [Environment]::GetEnvironmentVariable("Path", "User")
        $pathEntries = if ($currentUserPath) { $currentUserPath -split ';' } else { @() }
        if ($pathEntries -notcontains $InstallDir) {
            Write-Info "Adding $InstallDir to user PATH"
            $newPath = "$currentUserPath;$InstallDir".TrimStart(';')

            try {
                [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
            } catch [System.Security.SecurityException] {
                throw "Could not update PATH environment variable: $($_.Exception.Message)"
            }
            # Update current process PATH so cremio is available immediately
            $env:Path = "$env:Path;$InstallDir"
            Write-Info "PATH updated for this session."
            Write-Info "New terminal sessions will also have cremio on PATH."
        } else {
            Write-Info "$InstallDir is already on user PATH."
        }
    }

    # 7. Verify
    Write-Step "Verifying installation..."
    if (Test-Path $dest) {
        $size = (Get-Item $dest).Length
        Write-Info "cremio.exe is present ($([math]::Round($size/1MB, 1)) MB)"
    } else {
        throw "Installation verification failed: $dest not found"
    }

    Write-Host ""
    if ($existingVersion) {
        Write-Host "  Cremio updated v$($existingVersion.TrimStart('v')) -> $tag successfully!" -ForegroundColor Green
    } else {
        Write-Host "  Cremio $tag installed successfully!" -ForegroundColor Green
    }
    Write-Host "  Run 'cremio' in a new terminal to get started." -ForegroundColor Cyan
    Write-Host "  Re-run this script anytime to check for updates." -ForegroundColor DarkGray
    Write-Host ""
}

try {
    Install-Cremio
} catch {
    Write-Host ""
    Write-Host "  ERROR: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host ""
    exit 1
}
