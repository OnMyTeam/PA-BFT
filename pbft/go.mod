module main

go 1.14

require (
	consensus v0.0.0
	network v0.0.0
)

replace (
	consensus v0.0.0 => ./consensus
	network v0.0.0 => ./network
)
