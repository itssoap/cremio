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
        try {
            return Invoke-RestMethod -Uri $api -Method Get -ErrorAction Stop
        } catch {
            Write-Info "GitHub API call failed ($_), retrying..."
            try {
                return Invoke-RestMethod -Uri $api -Method Get -ErrorAction Stop
            } catch {
                throw "Could not reach GitHub API. Check your internet connection and try again.`n$($_.Exception.Message)"
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

    # 2. Check if already installed and up-to-date
    $installedTag = $null
    if (Test-Path $VersionFile) {
        $installedTag = (Get-Content $VersionFile -Raw).Trim()
    }

    if ($installedTag -and (Test-Path (Join-Path $InstallDir $Binary))) {
        if ($installedTag -eq $tag) {
            Write-Host ""
            Write-Host "  Cremio $installedTag is already up to date at:" -ForegroundColor DarkGreen
            Write-Info $InstallDir
            Write-Host ""
            return
        }
        Write-Step "New version available: $tag (installed: $installedTag)"
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
    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }

    $dest = Join-Path $InstallDir $Binary

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

    # 6. Add to PATH
    if (-not $NoPath) {
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
    Write-Host "  Cremio $tag installed successfully!" -ForegroundColor Green
    Write-Host "  Run 'cremio' in a new terminal to get started." -ForegroundColor Cyan
    Write-Host "  Re-run this script anytime to check for updates." -ForegroundColor DarkGray
    Write-Host ""
}

Install-Cremio
