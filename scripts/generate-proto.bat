@echo off
setlocal

set PROTOC=C:\Users\montf\AppData\Local\Microsoft\WinGet\Packages\Google.Protobuf_Microsoft.Winget.Source_8wekyb3d8bbwe\bin\protoc.exe
set PATH=C:\Users\montf\go\bin;%PATH%

cd /d D:\recording

echo Generating Go proto files...
%PROTOC% --proto_path=proto --go_out=proto --go_opt=paths=source_relative --go-grpc_out=proto --go-grpc_opt=paths=source_relative proto/recording_sfu.proto

if %ERRORLEVEL% EQU 0 (
    echo Proto files generated successfully!
    dir proto\*.pb.go
) else (
    echo Failed to generate proto files
    exit /b 1
)

endlocal
