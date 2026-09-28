$ErrorActionPreference = "Stop"

Push-Location $PSScriptRoot
go build -ldflags "-H windowsgui" -o vbrouter.exe .
$buildExitCode = $LASTEXITCODE
Pop-Location
exit $buildExitCode
