// The website is not Go. This file marks web/ as a module of its own so
// that ./... in the conch module stops here: go doesn't skip node_modules,
// and a package an npm dependency ships (flatted has one) would otherwise
// be vetted, linted and tested as part of conch.
module github.com/Amitgb14/conch/web
