@echo off
chcp 65001 >nul
set PYTHONIOENCODING=utf-8
title 散户 sanhu — 安装向导
cd /d "%~dp0"

echo ================================================
echo   散户 sanhu — 安装向导
echo ================================================
echo.

:: ── Step 1: 检测 Python ───────────────────────────────────────────────────
python --version >nul 2>&1
if errorlevel 1 (
    echo [FAIL] 未检测到 Python
    echo.
    echo 请先安装 Python 3.9 或以上版本：
    echo   https://www.python.org/downloads/
    echo.
    echo 安装时请勾选 "Add Python to PATH"
    echo.
    pause
    exit /b 1
)

for /f "tokens=2" %%v in ('python --version 2^>^&1') do set PYVER=%%v
echo [OK] Python %PYVER%
echo.

:: ── Step 2: 安装依赖 ──────────────────────────────────────────────────────
echo [1/3] 安装依赖包（首次约需 2-5 分钟）...
python -m pip install -r requirements.txt -q --disable-pip-version-check
if errorlevel 1 (
    echo [FAIL] 依赖安装失败，请检查网络连接后重试
    pause
    exit /b 1
)
echo [OK] 依赖安装完成
echo.

:: ── Step 3: 初始化数据库 ──────────────────────────────────────────────────
echo [2/3] 初始化数据库...
echo y | python scripts/init_db.py
if errorlevel 1 (
    echo [FAIL] 数据库初始化失败
    pause
    exit /b 1
)
echo.

:: ── Step 4: 启动服务 ──────────────────────────────────────────────────────
echo [3/3] 启动服务...
echo.
echo ================================================
echo   [OK] 安装完成！
echo.
echo   浏览器将自动打开：
echo   http://localhost:8000
echo.
echo   关闭此窗口即停止服务
echo ================================================
echo.

:: 延迟后打开浏览器
start "" timeout /t 2 /nobreak >nul
start "" "http://localhost:8000"

python app.py
pause
