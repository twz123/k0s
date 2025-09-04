# --- Pin route to WSL API and keep it while HNS settles ---
$ErrorActionPreference = 'Stop'

param(
    [string]$LogFile = ""
)

function Log($message) {
  $msg = "[$(Get-Date -Format o)] $message"
  if ($LogFile) {
    # Log to file
    $msg | Add-Content -Path $LogFile -Encoding utf8
  } else {
    # Log to stdout
    Write-Output $msg
  }
}

function Get-WslIPv4 {
  (wsl.exe -e sh -lc "hostname -I | cut -f 1 -d ' '").Trim()
}

function Get-BestRoute($ip) {
  Log "Getting best route for $ip"
  if (Get-Command Find-NetRoute -ErrorAction SilentlyContinue) {
    Find-NetRoute -RemoteIPAddress $ip | Select-Object -First 1
  } else {
    $m = route.exe PRINT $ip | Select-String -Pattern '^\s*\d+\.\d+\.\d+\.\d+\s+\d+\.\d+\.\d+\.\d+\s+\d+\.\d+\.\d+\.\d+\s+(\d+)\s+\d+' -AllMatches
    if ($m.Matches.Count -gt 0) {
      $ifIndex = [int]$m.Matches[-1].Groups[1].Value
      [pscustomobject]@{ InterfaceIndex = $ifIndex; NextHop = '0.0.0.0' }
    } else { $null }
  }
  Log "Best route for $ip is via IF $($best.InterfaceIndex) NextHop $($best.NextHop)"
}

function Pin-HostRoute($ip, $ifIndex, $nextHop) {
  Log "Pinning host route for $ip via IF $ifIndex (NextHop: $nextHop)"
  Get-NetRoute -DestinationPrefix "$ip" -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue
  New-NetRoute -DestinationPrefix "$ip" -InterfaceIndex $ifIndex -NextHop $nextHop -RouteMetric 1 -PolicyStore PersistentStore | Out-Null
}

# 1) Current WSL IP (inside WSL)
$wslIp = Get-WslIPv4
if (-not $wslIp) { throw 'WSL IP not found' }
Log "Found WSL IP: $wslIp"

# 2) Best route/interface for that IP
$wslRoute = Get-BestRoute $wslIp
if (-not $wslRoute) { throw "Could not determine route to $wslIp" }
$ifIndex = $wslRoute.InterfaceIndex
$nextHop = if ($wslRoute.NextHop) { $wslRoute.NextHop } else { '0.0.0.0' }
$ifName  = (Get-NetAdapter -InterfaceIndex $ifIndex).Name
$defaultRoute = Get-BestRoute '1.1.1.1'

Log "Starting WSL route watchdog loop"
while($true) {
  $ipNow = Get-WslIPv4
  if ($ipNow -ne $wslIp) {
    Log "WSL IP changed from $wslIp to $ipNow"
    $wslIp = $ipNow
  }

  # Ensure we still have route to the WSL IP
  $r = Get-NetRoute -DestinationPrefix "$wslIp/32" -ErrorAction SilentlyContinue
  if (-not $r) {
    Pin-HostRoute -ip "$wslIp/32" -ifIndex $ifIndex -nextHop $nextHop
  } else {
    Log "Still have WSL route"
  }
  # Ensure we still have a default route
  $r = Get-NetRoute -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue
  if (-not $r) {
    Pin-HostRoute -ip '0.0.0.0/0' -ifIndex $defaultRoute.InterfaceIndex -nextHop $defaultRoute.NextHop
  } else {
    Log "Still have default route"
  }
  Log "Testing connectivity to kube api on WSL IP"
  Test-NetConnection -ComputerName $wslIp -Port 6443 | Out-Host

  Start-Sleep -Seconds 2
}



