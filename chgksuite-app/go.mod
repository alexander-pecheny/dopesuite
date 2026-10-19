module code.pecheny.me/pecheny/dopesuite/chgksuite-app

go 1.26.4

replace xy => ../xy

replace pecheny.me/dopeuikit => ../dopeuikit

replace code.pecheny.me/pecheny/dopesuite/dopecore => ../dopecore

replace code.pecheny.me/pecheny/dopesuite/chgksuite => ../chgksuite

require (
	code.pecheny.me/pecheny/dopesuite/chgksuite v0.0.0
	github.com/wailsapp/wails/v3 v3.0.0-00010101000000-000000000000
	xy v0.0.0
)

require (
	code.pecheny.me/pecheny/dopesuite/dopecore v0.0.0 // indirect
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hhrutter/lzw v1.0.0 // indirect
	github.com/hhrutter/pkcs7 v0.2.2 // indirect
	github.com/hhrutter/tiff v1.0.3 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pdfcpu/pdfcpu v0.13.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/tdewolff/minify/v2 v2.24.17 // indirect
	github.com/tdewolff/parse/v2 v2.8.16 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	github.com/ulikunitz/xz v0.5.16 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/image v0.43.0 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	modernc.org/libc v1.72.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.50.0 // indirect
	pecheny.me/dopeuikit v0.0.0 // indirect
)

replace github.com/wailsapp/wails/v3 => code.pecheny.me/pecheny/wails/v3 v3.0.0-20261019153355-d73831f9cca1
