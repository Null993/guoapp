param([Parameter(Mandatory=$true)][string]$PlanPath)
$ErrorActionPreference = 'Stop'
$plan = Get-Content -LiteralPath $PlanPath -Raw | ConvertFrom-Json
$utf8 = New-Object System.Text.UTF8Encoding($false)
function Write-Json($Path, $Value) {
    [System.IO.File]::WriteAllText($Path, ($Value | ConvertTo-Json -Depth 10), $utf8)
}
function Child-Path($Root, $Name) {
    if ([string]::IsNullOrWhiteSpace($Name) -or $Name -match '(^[/\\]|:|(^|[/\\])\.\.?([/\\]|$))') { throw 'Unsafe update path' }
    $base = [System.IO.Path]::GetFullPath($Root).TrimEnd('\') + '\'
    $full = [System.IO.Path]::GetFullPath([System.IO.Path]::Combine($Root, $Name))
    if (!$full.StartsWith($base, [System.StringComparison]::OrdinalIgnoreCase)) { throw 'Update path escaped root' }
    $check = $full
    while ($check.Length -ge $base.TrimEnd('\').Length) {
        if ((Test-Path -LiteralPath $check) -and ((Get-Item -LiteralPath $check -Force).Attributes -band [System.IO.FileAttributes]::ReparsePoint)) { throw 'Update cannot follow links' }
        $parent = Split-Path -Parent $check
        if ($parent -eq $check -or !$parent) { break }
        $check = $parent
    }
    return $full
}
$moved = New-Object System.Collections.Generic.List[string]
$copied = New-Object System.Collections.Generic.List[string]
$started = $null
try {
    foreach ($name in @($plan.files) + @($plan.oldFiles)) { $null = Child-Path $plan.target $name; $null = Child-Path $plan.backup $name }
    foreach ($name in @($plan.files)) { $null = Child-Path $plan.stage $name }
    if ($plan.pid -gt 0 -and (Get-Process -Id $plan.pid -ErrorAction SilentlyContinue)) { Wait-Process -Id $plan.pid -Timeout 60 -ErrorAction Stop }
    $names = @(@($plan.oldFiles) + @($plan.files) | Sort-Object -Unique)
    foreach ($name in $names) {
        $from = Child-Path $plan.target $name
        if (Test-Path -LiteralPath $from -PathType Leaf) {
            $to = Child-Path $plan.backup $name
            $null = New-Item -ItemType Directory -Path (Split-Path -Parent $to) -Force
            Move-Item -LiteralPath $from -Destination $to
            $moved.Add($name)
        }
    }
    foreach ($name in @($plan.files)) {
        $from = Child-Path $plan.stage $name
        $to = Child-Path $plan.target $name
        $null = New-Item -ItemType Directory -Path (Split-Path -Parent $to) -Force
        $copied.Add($name)
        Copy-Item -LiteralPath $from -Destination $to -Force
    }
    $program = Child-Path $plan.target $plan.executable
    $started = Start-Process -FilePath $program -ArgumentList @('--update-token', $plan.token) -WorkingDirectory $plan.target -WindowStyle Hidden -PassThru
    $limit = [Math]::Min(120, [Math]::Max(2, [int]$plan.timeout))
    $deadline = [DateTime]::UtcNow.AddSeconds($limit)
    while (!(Test-Path -LiteralPath $plan.ready)) {
        if ($started.HasExited -or [DateTime]::UtcNow -ge $deadline) { throw 'New app did not confirm a healthy startup' }
        Start-Sleep -Milliseconds 250
        $started.Refresh()
    }
    $plan.oldFiles = @($moved)
    Write-Json $plan.history $plan
    Write-Json $plan.result @{status='installed';version=$plan.version;message='更新完成，已保留上一版本备份'}
} catch {
    $failure = $_.Exception.Message
    if ($started -and !$started.HasExited) { Stop-Process -Id $started.Id -Force -ErrorAction SilentlyContinue; Start-Sleep -Milliseconds 500 }
    $rollbackErrors = New-Object System.Collections.Generic.List[string]
    foreach ($name in $copied) {
        try { $to = Child-Path $plan.target $name; if (Test-Path -LiteralPath $to -PathType Leaf) { Remove-Item -LiteralPath $to -Force } } catch { $rollbackErrors.Add($_.Exception.Message) }
    }
    foreach ($name in $moved) {
        try { $from = Child-Path $plan.backup $name; $to = Child-Path $plan.target $name; $null = New-Item -ItemType Directory -Path (Split-Path -Parent $to) -Force; Move-Item -LiteralPath $from -Destination $to -Force } catch { $rollbackErrors.Add($_.Exception.Message) }
    }
    $message = '更新失败，已恢复原版本：' + $failure
    if ($rollbackErrors.Count -gt 0) { $message = '更新失败，自动恢复未完成，请从备份目录恢复：' + ($rollbackErrors -join '; ') }
    Write-Json $plan.result @{status='failed';message=$message;backup=$plan.backup}
    if ($moved.Count -gt 0 -and $rollbackErrors.Count -eq 0) { Start-Process -FilePath (Child-Path $plan.target $plan.executable) -WorkingDirectory $plan.target -WindowStyle Hidden }
    exit 1
}
