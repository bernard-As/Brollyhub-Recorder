$env:PATH = "C:\Users\montf\go\bin;" + $env:PATH

$protoc = "C:\Users\montf\AppData\Local\Microsoft\WinGet\Packages\Google.Protobuf_Microsoft.Winget.Source_8wekyb3d8bbwe\bin\protoc.exe"

Set-Location D:\recording

Write-Host "Generating Go proto files..."

& $protoc --proto_path=proto --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative proto/recording_sfu.proto proto/recording_shelves.proto

if ($LASTEXITCODE -eq 0) {
    Write-Host "Proto files generated successfully!"
    Get-ChildItem proto\*.pb.go
} else {
    Write-Host "Failed to generate proto files"
    exit 1
}
