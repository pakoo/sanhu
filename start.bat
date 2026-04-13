@echo off
chcp 65001 >nul
set PYTHONIOENCODING=utf-8
title 散户 sanhu
cd /d "%~dp0"

echo ================================================
echo   散户 sanhu 启动中...
echo   http://localhost:8000
echo   关闭此窗口即停止服务
echo ================================================
echo.

start "" timeout /t 2 /nobreak >nul
start "" "http://localhost:8000"

python app.py
pause
