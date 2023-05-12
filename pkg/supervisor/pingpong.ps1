#!/usr/bin/env pwsh

# Copyright 2023 k0s authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

Param([String] $Namespace)

$ErrorActionPreference = "Stop"
$stderr = [System.Console]::Error

$pong = [System.IO.Pipes.NamedPipeServerStream]::new((Join-Path -Path $Namespace -ChildPath 'pong'))
try {
    $stderr.WriteLine("${PID}: Sending ping")
    $ping = [System.IO.Pipes.NamedPipeClientStream]::new((Join-Path -Path $Namespace -ChildPath 'ping'))
    try {
        $ping.Connect()
        $writer = [System.IO.StreamWriter]::new($ping)
        $writer.WriteLine("ping from ${PID}")
        $writer.Flush()
        $ping.WaitForPipeDrain()
    }
    finally {
        $ping.Close()
        $ping.Dispose()
    }

    $stderr.WriteLine("${PID}: Awaiting pong")
    $pong.WaitForConnection()
    [System.IO.StreamReader]::new($pong).ReadToEnd() | Out-Null
}
finally {
    $pong.Close()
    $pong.Dispose()
}

$stderr.WriteLine("${PID}: Goodbye")
