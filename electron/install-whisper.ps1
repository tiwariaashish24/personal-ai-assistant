param(
  [string]$InstallDirectory = (Join-Path $env:APPDATA "JARVIS\voice"),
  [ValidateSet("Base", "Small", "Both")]
  [string]$Model = "Base",
  [switch]$Force
)

$ErrorActionPreference = "Stop"
$whisperVersion = "v1.9.6"
$modelSpecs = @{
  base = @{
    File = "ggml-base.bin"
    Url = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin?download=true"
    MinimumBytes = 100000000
    MaximumBytes = 200000000
    ApproximateMiB = 142
  }
  small = @{
    File = "ggml-small.bin"
    Url = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin?download=true"
    MinimumBytes = 400000000
    MaximumBytes = 600000000
    ApproximateMiB = 466
  }
}
$selectedModels = if ($Model -eq "Both") { @("base", "small") } else { @($Model.ToLowerInvariant()) }

if ([string]::IsNullOrWhiteSpace($env:APPDATA)) {
  throw "APPDATA is not set. Run this installer on Windows."
}
$workDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ("jarvis-whisper-" + [guid]::NewGuid().ToString("N"))
$binaryPath = Join-Path $InstallDirectory "whisper-cli.exe"
$localReleaseDirectory = Join-Path $PSScriptRoot "..\whisper\Release"
$localBinary = Join-Path $localReleaseDirectory "whisper-cli.exe"

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
  $binaryInfo = Get-Item $binaryPath -ErrorAction SilentlyContinue
  $modelsInstalled = $true
  foreach ($modelName in $selectedModels) {
    $spec = $modelSpecs[$modelName]
    $modelPath = Join-Path $InstallDirectory $spec.File
    $modelInfo = Get-Item $modelPath -ErrorAction SilentlyContinue
    if (-not $modelInfo -or $modelInfo.Length -lt $spec.MinimumBytes -or $modelInfo.Length -gt $spec.MaximumBytes) {
      $modelsInstalled = $false
    }
  }
  if (-not $Force -and $modelsInstalled -and $binaryInfo) {
    Test-WhisperExecutable $binaryPath
    Write-Output "Whisper.cpp and the requested multilingual model(s) are already installed in $InstallDirectory"
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

  foreach ($modelName in $selectedModels) {
    $spec = $modelSpecs[$modelName]
    $modelPath = Join-Path $InstallDirectory $spec.File
    $existingModel = Get-Item $modelPath -ErrorAction SilentlyContinue
    if (-not $Force -and $existingModel -and
        $existingModel.Length -ge $spec.MinimumBytes -and
        $existingModel.Length -le $spec.MaximumBytes) {
      continue
    }

    $localModel = Join-Path $PSScriptRoot "..\whisper\models\$($spec.File)"
    if (-not $Force -and (Test-Path $localModel -PathType Leaf)) {
      $localInfo = Get-Item $localModel
      if ($localInfo.Length -ge $spec.MinimumBytes -and $localInfo.Length -le $spec.MaximumBytes) {
        Write-Output "Installing the local multilingual Whisper $modelName model..."
        Copy-Item $localModel "$modelPath.new" -Force
        Move-Item "$modelPath.new" $modelPath -Force
        continue
      }
    }

    $downloadPath = Join-Path $workDirectory $spec.File
    $approximateMiB = $spec.ApproximateMiB
    Write-Output "Downloading the multilingual Whisper $modelName model (about $approximateMiB MiB)..."
    Invoke-WebRequest -UseBasicParsing -Uri $spec.Url -OutFile $downloadPath
    $downloadedModel = Get-Item $downloadPath
    if ($downloadedModel.Length -lt $spec.MinimumBytes -or $downloadedModel.Length -gt $spec.MaximumBytes) {
      throw "The downloaded Whisper $modelName model has an unexpected size ($($downloadedModel.Length) bytes)."
    }
    Move-Item $downloadPath $modelPath -Force
  }

  Test-WhisperExecutable $binaryPath
  Write-Output "Whisper.cpp is ready: $binaryPath"
  foreach ($modelName in $selectedModels) {
    Write-Output "Multilingual model is ready: $(Join-Path $InstallDirectory $modelSpecs[$modelName].File)"
  }
} finally {
  if (Test-Path $workDirectory) {
    Remove-Item -LiteralPath $workDirectory -Recurse -Force
  }
}
