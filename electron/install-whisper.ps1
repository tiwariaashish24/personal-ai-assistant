param(
  [string]$InstallDirectory = (Join-Path $env:APPDATA "JARVIS\voice"),
  [switch]$Force
)

$ErrorActionPreference = "Stop"
$whisperVersion = "v1.9.6"
$modelUrl = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin?download=true"

if ([string]::IsNullOrWhiteSpace($env:APPDATA)) {
  throw "APPDATA is not set. Run this installer on Windows."
}
$workDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ("jarvis-whisper-" + [guid]::NewGuid().ToString("N"))
$modelPath = Join-Path $InstallDirectory "ggml-base.bin"
$binaryPath = Join-Path $InstallDirectory "whisper-cli.exe"
$localReleaseDirectory = Join-Path $PSScriptRoot "..\whisper\Release"
$localBinary = Join-Path $localReleaseDirectory "whisper-cli.exe"
$localModel = Join-Path $PSScriptRoot "..\whisper\models\ggml-base.bin"

function Test-WhisperExecutable([string]$Path) {
  $stdoutPath = Join-Path $env:TEMP ("jarvis-whisper-" + [guid]::NewGuid().ToString("N") + ".out")
  $stderrPath = "$stdoutPath.err"
  try {
    $process = Start-Process -FilePath $Path -ArgumentList "-h" -NoNewWindow -Wait -PassThru `
      -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath
    if ($process.ExitCode -ne 0) {
      $diagnostic = Get-Content $stderrPath -Raw -ErrorAction SilentlyContinue
      throw "whisper-cli.exe failed its startup check (exit $($process.ExitCode)): $diagnostic"
    }
  } finally {
    Remove-Item -LiteralPath $stdoutPath, $stderrPath -Force -ErrorAction SilentlyContinue
  }
}

New-Item -ItemType Directory -Path $InstallDirectory -Force | Out-Null

try {
  $modelInfo = Get-Item $modelPath -ErrorAction SilentlyContinue
  $binaryInfo = Get-Item $binaryPath -ErrorAction SilentlyContinue
  if (-not $Force -and $modelInfo -and $modelInfo.Length -ge 100000000 -and $binaryInfo) {
    Test-WhisperExecutable $binaryPath
    Write-Output "Whisper.cpp and the multilingual base model are already installed in $InstallDirectory"
    exit 0
  }

  New-Item -ItemType Directory -Path $workDirectory | Out-Null
  if ($Force -or -not $binaryInfo) {
    if (-not $Force -and (Test-Path $localBinary -PathType Leaf)) {
      Write-Output "Using the local whisper.cpp Windows bundle..."
      Copy-Item $localBinary "$binaryPath.new" -Force
      Move-Item "$binaryPath.new" $binaryPath -Force
      Get-ChildItem -Path $localReleaseDirectory -Filter "*.dll" -File |
        Copy-Item -Destination $InstallDirectory -Force
    } else {
      if (-not (Get-Command cmake -ErrorAction SilentlyContinue)) {
        throw "CMake is required. Install CMake and Visual Studio Build Tools with the C++ workload, then retry."
      }
      $archivePath = Join-Path $workDirectory "whisper.cpp.zip"
      $sourceUrl = "https://github.com/ggml-org/whisper.cpp/archive/refs/tags/$whisperVersion.zip"
      Write-Output "Downloading official whisper.cpp source ($whisperVersion)..."
      Invoke-WebRequest -UseBasicParsing -Uri $sourceUrl -OutFile $archivePath
      Expand-Archive -Path $archivePath -DestinationPath $workDirectory
      $sourceDirectory = Get-ChildItem -Path $workDirectory -Directory |
        Where-Object { $_.Name -like "whisper.cpp-*" } |
        Select-Object -First 1
      if (-not $sourceDirectory) {
        throw "The official whisper.cpp source archive did not contain its expected source folder."
      }

      $buildDirectory = Join-Path $workDirectory "build"
      & cmake -S $sourceDirectory.FullName -B $buildDirectory -A x64 `
        -DWHISPER_BUILD_IS_DEV=OFF `
        -DWHISPER_BUILD_TESTS=OFF `
        -DWHISPER_BUILD_EXAMPLES=ON `
        -DWHISPER_BUILD_SERVER=OFF `
        -DBUILD_SHARED_LIBS=OFF `
        -DGGML_CUDA=OFF `
        -DGGML_NATIVE=ON
      if ($LASTEXITCODE -ne 0) {
        throw "CMake configuration failed. Confirm the Visual Studio C++ build tools are installed."
      }

      Write-Output "Building CPU-optimized whisper-cli (4 build workers)..."
      & cmake --build $buildDirectory --config Release --target whisper-cli --parallel 4
      if ($LASTEXITCODE -ne 0) {
        throw "whisper-cli build failed."
      }
      $builtBinary = Join-Path $buildDirectory "bin\whisper-cli.exe"
      if (-not (Test-Path $builtBinary -PathType Leaf)) {
        throw "The whisper-cli build completed without producing whisper-cli.exe."
      }
      Copy-Item $builtBinary "$binaryPath.new" -Force
      Move-Item "$binaryPath.new" $binaryPath -Force
    }
  }

  $existingModel = Get-Item $modelPath -ErrorAction SilentlyContinue
  if ($Force -or -not $existingModel -or $existingModel.Length -lt 100000000) {
    if (-not $Force -and (Test-Path $localModel -PathType Leaf) -and (Get-Item $localModel).Length -ge 100000000) {
      Write-Output "Installing the local multilingual Whisper base model..."
      Copy-Item $localModel "$modelPath.new" -Force
      Move-Item "$modelPath.new" $modelPath -Force
    } else {
      $downloadPath = Join-Path $workDirectory "ggml-base.bin"
      Write-Output "Downloading the multilingual Whisper base model (about 142 MiB)..."
      Invoke-WebRequest -UseBasicParsing -Uri $modelUrl -OutFile $downloadPath
      $downloadedModel = Get-Item $downloadPath
      if ($downloadedModel.Length -lt 100000000 -or $downloadedModel.Length -gt 200000000) {
        throw "The downloaded Whisper model has an unexpected size ($($downloadedModel.Length) bytes)."
      }
      Move-Item $downloadPath $modelPath -Force
    }
  }

  Test-WhisperExecutable $binaryPath
  Write-Output "Whisper.cpp is ready: $binaryPath"
  Write-Output "Multilingual model is ready: $modelPath"
} finally {
  if (Test-Path $workDirectory) {
    Remove-Item -LiteralPath $workDirectory -Recurse -Force
  }
}
