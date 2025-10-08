<!--
SPDX-FileCopyrightText: 2025 k0s authors

SPDX-License-Identifier: CC-BY-SA-4.0
-->

It's a pity that UNIX shells won't like CRLF and Windows PowerShell won't like
LF.

## Check PowerShell version

In a PowerShell session, just type `$PSVersionTable.PSVersion`.

## Install latest PowerShell manually

```powershell
# Turn off progress, as updating it is ridiculously slow
$ProgressPreference = 'SilentlyContinue'
# Stop on errors
$ErrorActionPreference = 'Stop'

# Get the latest PowerShell version
$latestVersion = (Invoke-RestMethod -Uri "https://api.github.com/repos/PowerShell/PowerShell/releases/latest").tag_name.TrimStart("v")

# Download and install it
$src = "https://github.com/PowerShell/PowerShell/releases/latest/download/PowerShell-$latestVersion-win-x64.msi"
$dst = "$env:TEMP\pwsh-$latestVersion.msi"

Write-Host "Latest PowerShell version: $latestVersion"
Write-Host "Download URL: $src"

Invoke-WebRequest -Uri $src -OutFile $dst
Start-Process msiexec.exe -Wait -ArgumentList "/i `"$dst`" /quiet /norestart"

# Check that the installation was successful
Get-Item "C:\Program Files\PowerShell\7\pwsh.exe"
```

## Install Containers feature

```pwsh
Install-WindowsFeature -Name Containers
Restart-Computer -Force
```

## Set the default SSH shell to be PowerShell

```powershell
$path = (Get-Command pwsh).Path
New-ItemProperty -Path HKLM:\SOFTWARE\OpenSSH -Name DefaultShell -PropertyType String -Value "$path" -Force
```

<!-- Set-PSReadlineKeyHandler -Key Tab -Function MenuComplete -->

## Use PowerShell remote sessions

See [PowerShell remoting over SSH] on Microsoft Learn.

Enable the subsystem on the host:

```powershell
$sshdConfigPath = "C:\ProgramData\ssh\sshd_config"

# Load config and print it
$config = Get-Content $sshdConfigPath

# We need the short 8.3 style path to Powershell
# https://github.com/PowerShell/Win32-OpenSSH/issues/784
$pwshPath = (New-Object -ComObject Scripting.FileSystemObject).GetFile((Get-Command pwsh).Path).ShortPath

# Build the powershell subsystem line
$subsystemLine = "Subsystem powershell $pwshPath -sshs"

# Replace existing line or append if not found
if ($config -match '^Subsystem powershell') {
    $config = $config -replace '^Subsystem powershell.*', $subsystemLine
} else {
    $config += $subsystemLine
}

# Print the modified config
echo $config

# If you're okay with it, write it back to the file
Set-Content -Path $sshdConfigPath -Value $config

# Restart OpenSSH
Restart-Service sshd
```

Then, from a client PowerShell session:

```powershell
Enter-PSSession -HostName <...> -UserName <...> -KeyFilePath <...>
```

[PowerShell remoting over SSH]: https://learn.microsoft.com/en-us/powershell/scripting/security/remoting/ssh-remoting-in-powershell

## Replacement for `top`

```powershell
while ($true) {
    Clear-Host
    Get-Process |
        Sort-Object CPU -Descending |
        Select-Object -First 10 ProcessName, CPU, Id, WorkingSet |
        Format-Table -AutoSize
    Start-Sleep -Seconds 2
}
```

## `less` for Windows

```powershell
$ErrorActionPreference = 'Stop' # Stop on errors

$src = "https://github.com/jftuga/less-Windows/releases/download/less-v679/less-x64.zip"
$dst = "$env:TEMP\less.zip"

Invoke-WebRequest -ProgressAction SilentlyContinue -Uri $src -OutFile $dst
Expand-Archive -Path $dst -DestinationPath (Join-Path $env:LOCALAPPDATA 'Microsoft\WindowsApps') -Force
```

## Install k0s worker

```powershell
# #Get-WindowsFeature -Name Containers
Install-WindowsFeature -Name Containers

# Get-NetFirewallProfile | Format-Table -Property Name, Enabled
Set-NetFirewallProfile -Profile Domain, Private, Public -Enabled False

Move-Item -Path k0s.exe -Destination (Join-Path $env:LOCALAPPDATA 'Microsoft\WindowsApps')
& k0s install worker --token-file \path\to\token-file --debug
& k0s start
```

## View k0s logs

```pwsh
for ($lastRecordId = 0; $true; ) {
    $filter = @{
        LogName      = 'Application'
        ProviderName = 'k0sworker'
        #StartTime    = (Get-Date).AddSeconds(-10)
    }

    $newEntries = Get-WinEvent -FilterHashtable $filter |
        Where-Object { $_.RecordId -gt $lastRecordId } |
        Sort-Object RecordId

    foreach ($entry in $newEntries) {
        $template = "[{0}] {1} - {2} - {3}"
        $message = $entry.Message -replace '\s+', ' '
        $formatted = $template -f $entry.TimeCreated, $entry.LevelDisplayName, $entry.ProviderName, $message
        Write-Output $formatted

        $lastRecordId = $entry.RecordId
    }

    Start-Sleep -Seconds 2
}
```

## Debug k0s with delve

```pwsh
$ErrorActionPreference = 'Stop'

# Install Go
$src = 'https://go.dev/dl/go1.25.1.windows-amd64.msi'
$dst = 'go1.25.1.windows-amd64.msi'
Invoke-WebRequest -ProgressAction SilentlyContinue -Uri $src -OutFile $dst
Start-Process msiexec.exe -Wait -ArgumentList @('/i', $dst, '/quiet', '/norestart')

# After relogin
go install github.com/go-delve/delve/cmd/dlv@latest
# Get-NetFirewallProfile | Format-Table -Property Name, Enabled
# Disable firewall, so that delve can accept connections.
Set-NetFirewallProfile -Profile Domain, Public, Private -Enabled False
dlv --listen=:2345 --headless=true --api-version=2 exec -- (Get-Command k0s).Path worker
```
