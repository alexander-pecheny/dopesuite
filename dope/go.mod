module dope

go 1.26.4

require (
	github.com/klauspost/compress v1.18.6
	github.com/xuri/excelize/v2 v2.10.1
	github.com/yuin/goldmark v1.8.2
	modernc.org/sqlite v1.50.0
	pecheny.me/dopeuikit v0.0.0
)

require golang.org/x/crypto v0.52.0 // indirect

replace pecheny.me/dopeuikit => ../dopeuikit

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/richardlehane/mscfb v1.0.6 // indirect
	github.com/richardlehane/msoleps v1.0.6 // indirect
	github.com/tiendc/go-deepcopy v1.7.2 // indirect
	github.com/xuri/efp v0.0.1 // indirect
	github.com/xuri/nfp v0.0.2-0.20250530014748-2ddeb826f9a9 // indirect
	golang.org/x/net v0.54.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	modernc.org/libc v1.72.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	pecheny.me/dopecore v0.0.0
)

replace pecheny.me/dopecore => ../dopecore
