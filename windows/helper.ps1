# Thirai helper: one long-running Windows PowerShell 5.1 process.
# Reads one command per line on stdin and writes exactly one line back on stdout.
#   apps          -> JSON [{name, path}] from the Start menu (desktop and Store apps)
#   icon <b64>    -> base64 PNG of an app's icon (arg: base64 UTF-8 AppID), or empty
#   np            -> JSON now playing (Windows media controls: Spotify, Apple Music, browsers...)
#   art           -> base64 of the current track's thumbnail, or empty
#   bt            -> JSON [{name, level}] for Bluetooth devices that report a battery level
# Anything that fails returns an empty line, so the deck degrades instead of breaking.

$ErrorActionPreference = 'Stop'
$utf8 = New-Object System.Text.UTF8Encoding $false
[Console]::OutputEncoding = $utf8
[Console]::InputEncoding = $utf8

function Reply([string]$s) {
    if ($null -eq $s) { $s = '' }
    [Console]::Out.WriteLine(($s -replace "[`r`n]", ''))
    [Console]::Out.Flush()
}

# ---------- icons (IShellItemImageFactory works for desktop and Store apps alike) ----------
$iconCode = @'
using System;
using System.Drawing;
using System.Drawing.Imaging;
using System.IO;
using System.Runtime.InteropServices;

public static class DeckIcon {
    [ComImport, Guid("bcc18b79-ba16-442f-80c4-8a59c30c463b"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface IShellItemImageFactory { [PreserveSig] int GetImage(SIZE size, int flags, out IntPtr phbm); }

    [StructLayout(LayoutKind.Sequential)] struct SIZE { public int cx; public int cy; }

    [StructLayout(LayoutKind.Sequential)] struct BITMAP {
        public int bmType, bmWidth, bmHeight, bmWidthBytes; public ushort bmPlanes, bmBitsPixel; public IntPtr bmBits; }

    [StructLayout(LayoutKind.Sequential)] struct BITMAPINFOHEADER {
        public int biSize, biWidth, biHeight; public ushort biPlanes, biBitCount;
        public int biCompression, biSizeImage, biXPelsPerMeter, biYPelsPerMeter, biClrUsed, biClrImportant; }

    [StructLayout(LayoutKind.Sequential)] struct DIBSECTION {
        public BITMAP dsBm; public BITMAPINFOHEADER dsBmih;
        public int bf0, bf1, bf2; public IntPtr dshSection; public int dsOffset; }

    [DllImport("shell32.dll", CharSet = CharSet.Unicode, PreserveSig = false)]
    static extern void SHCreateItemFromParsingName(string path, IntPtr pbc,
        [MarshalAs(UnmanagedType.LPStruct)] Guid riid, [MarshalAs(UnmanagedType.Interface)] out IShellItemImageFactory ppv);

    [DllImport("gdi32.dll")] static extern int GetObject(IntPtr h, int size, ref DIBSECTION ds);
    [DllImport("gdi32.dll")] static extern bool DeleteObject(IntPtr h);

    public static string Png(string parsingName, int size) {
        IShellItemImageFactory f;
        SHCreateItemFromParsingName(parsingName, IntPtr.Zero, typeof(IShellItemImageFactory).GUID, out f);
        SIZE s; s.cx = size; s.cy = size;
        IntPtr hbm;
        // SIIGBF_ICONONLY (4) | SIIGBF_BIGGERSIZEOK (1)
        if (f.GetImage(s, 5, out hbm) != 0 || hbm == IntPtr.Zero) return "";
        try {
            Bitmap bmp = null;
            DIBSECTION ds = new DIBSECTION();
            if (GetObject(hbm, Marshal.SizeOf(typeof(DIBSECTION)), ref ds) != 0
                && ds.dsBm.bmBitsPixel == 32 && ds.dsBm.bmBits != IntPtr.Zero) {
                // Keep the alpha channel: wrap the DIB's pixels directly (premultiplied ARGB).
                using (var view = new Bitmap(ds.dsBm.bmWidth, ds.dsBm.bmHeight, ds.dsBm.bmWidthBytes,
                                             PixelFormat.Format32bppPArgb, ds.dsBm.bmBits)) {
                    bmp = new Bitmap(view.Width, view.Height, PixelFormat.Format32bppArgb);
                    using (var g = Graphics.FromImage(bmp)) g.DrawImage(view, 0, 0, view.Width, view.Height);
                }
                if (ds.dsBmih.biHeight > 0) bmp.RotateFlip(RotateFlipType.RotateNoneFlipY);   // bottom-up DIB
            } else {
                bmp = Image.FromHbitmap(hbm);
            }
            using (bmp) using (var ms = new MemoryStream()) {
                bmp.Save(ms, ImageFormat.Png);
                return Convert.ToBase64String(ms.ToArray());
            }
        } finally { DeleteObject(hbm); }
    }
}
'@
$iconsReady = $false
try {
    Add-Type -TypeDefinition $iconCode -ReferencedAssemblies System.Drawing
    $iconsReady = $true
} catch { }

# ---------- WinRT media controls ----------
$mediaReady = $false
try {
    $null = Add-Type -AssemblyName System.Runtime.WindowsRuntime
    $null = [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager,Windows.Media.Control,ContentType=WindowsRuntime]
    $null = [Windows.Storage.Streams.IRandomAccessStreamWithContentType,Windows.Storage.Streams,ContentType=WindowsRuntime]
    $asTask = [System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object {
        $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1'
    } | Select-Object -First 1
    function Await($op, [Type]$type) {
        $task = $asTask.MakeGenericMethod($type).Invoke($null, @($op))
        if (-not $task.Wait(3000)) { throw 'timeout' }
        return $task.Result
    }
    $mgr = Await ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]::RequestAsync()) `
        ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager])
    $mediaReady = $true
} catch { }

function AppLabel([string]$aumid) {
    $a = $aumid.ToLower()
    if ($a -like '*spotify*') { return 'Spotify' }
    if ($a -like '*applemusic*' -or $a -like '*itunes*') { return 'Apple Music' }
    if ($a -like '*chrome*') { return 'Chrome' }
    if ($a -like '*msedge*' -or $a -like '*microsoftedge*') { return 'Edge' }
    if ($a -like '*firefox*') { return 'Firefox' }
    if ($a -like '*zunemusic*' -or $a -like '*media.player*') { return 'Media Player' }
    if ($a -like '*vlc*') { return 'VLC' }
    $name = ($aumid -split '!')[0] -replace '\.exe$', ''
    $name = ($name -split '[\\_]')[0]
    if ($name -like '*.*') { $name = ($name -split '\.')[-1] }
    return $name
}

$script:session = $null

function NowPlaying {
    if (-not $mediaReady) { return '{}' }
    $s = $mgr.GetCurrentSession()
    $script:session = $s
    if ($null -eq $s) { return '{}' }
    $props = Await ($s.TryGetMediaPropertiesAsync()) ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionMediaProperties])
    $pb = $s.GetPlaybackInfo()
    $tl = $s.GetTimelineProperties()
    $playing = ([int]$pb.PlaybackStatus -eq 4)
    $pos = $tl.Position.TotalSeconds
    if ($playing -and $tl.LastUpdatedTime.Year -gt 2000) {
        $pos += ([DateTimeOffset]::Now - $tl.LastUpdatedTime).TotalSeconds
    }
    $dur = ($tl.EndTime - $tl.StartTime).TotalSeconds
    if ($dur -gt 0 -and $pos -gt $dur) { $pos = $dur }
    $o = [ordered]@{
        app      = (AppLabel $s.SourceAppUserModelId)
        aumid    = $s.SourceAppUserModelId
        playing  = $playing
        title    = [string]$props.Title
        artist   = [string]$props.Artist
        album    = [string]$props.AlbumTitle
        duration = [math]::Max(0, $dur)
        position = [math]::Max(0, $pos)
        hasThumb = ($null -ne $props.Thumbnail)
    }
    if (-not $o.title) { return '{}' }
    return (ConvertTo-Json -InputObject $o -Compress)
}

function Artwork {
    if (-not $mediaReady) { return '' }
    $s = $mgr.GetCurrentSession()
    if ($null -eq $s) { return '' }
    $props = Await ($s.TryGetMediaPropertiesAsync()) ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionMediaProperties])
    if ($null -eq $props.Thumbnail) { return '' }
    $stream = Await ($props.Thumbnail.OpenReadAsync()) ([Windows.Storage.Streams.IRandomAccessStreamWithContentType])
    try {
        $net = [System.IO.WindowsRuntimeStreamExtensions]::AsStreamForRead($stream)
        $ms = New-Object System.IO.MemoryStream
        $net.CopyTo($ms)
        return [Convert]::ToBase64String($ms.ToArray())
    } finally { $stream.Dispose() }
}

function Apps {
    $skip = 'uninstall|readme|read me|help|documentation|release notes|website|license|changelog|support|manual'
    $list = @(Get-StartApps | Where-Object { $_.AppID -and $_.Name -and $_.Name -notmatch $skip } |
        Sort-Object Name -Unique | ForEach-Object { [ordered]@{ name = $_.Name; path = $_.AppID } })
    return (ConvertTo-Json -InputObject $list -Compress)
}

function Bluetooth {
    $key = '{104EA319-6EE2-4701-BD47-8DDBF425BBE5} 2'   # DEVPKEY_Bluetooth_Battery
    $devs = @(Get-PnpDevice -Class Bluetooth -PresentOnly -ErrorAction SilentlyContinue | Where-Object { $_.Status -eq 'OK' })
    $out = @()
    if ($devs.Count -gt 0) {
        $props = @(Get-PnpDeviceProperty -InstanceId ($devs | ForEach-Object { $_.InstanceId }) -KeyName $key -ErrorAction SilentlyContinue)
        $seen = @{}
        foreach ($p in $props) {
            if ($null -eq $p.Data -or $p.Type -eq 'Empty') { continue }
            $d = $devs | Where-Object { $_.InstanceId -eq $p.InstanceId } | Select-Object -First 1
            if ($null -eq $d -or $seen.ContainsKey($d.FriendlyName)) { continue }
            $seen[$d.FriendlyName] = $true
            $out += [ordered]@{ name = [string]$d.FriendlyName; level = [int]$p.Data }
        }
    }
    return (ConvertTo-Json -InputObject @($out) -Compress)
}

while ($true) {
    $line = [Console]::In.ReadLine()
    if ($null -eq $line) { break }
    $parts = $line.Split(' ', 2)
    $result = ''
    try {
        switch ($parts[0]) {
            'apps' { $result = Apps }
            'icon' {
                if ($iconsReady -and $parts.Count -gt 1) {
                    $id = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($parts[1]))
                    $result = [DeckIcon]::Png('shell:AppsFolder\' + $id, 128)
                }
            }
            'np' { $result = NowPlaying }
            'art' { $result = Artwork }
            'bt' { $result = Bluetooth }
            'ping' { $result = 'pong' }
        }
    } catch { $result = '' }
    Reply $result
}
