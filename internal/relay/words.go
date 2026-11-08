package relay

import "crypto/sha256"

func hashBytes(b []byte) [32]byte { return sha256.Sum256(b) }

// wordList is what a key's fingerprint is said in: 256 short, common,
// distinct words. The phone's app has the same list (ui/tunnel.mjs), and a
// test says they agree — words that differ between the two would read as
// a key that does.
var wordList = []string{
	"acorn", "amber", "anchor", "apple", "arrow", "aspen", "atlas", "badge",
	"bamboo", "banjo", "basil", "beacon", "bean", "bear", "beetle", "bell",
	"berry", "birch", "bison", "blaze", "bloom", "boat", "bolt", "bone",
	"brick", "brook", "brush", "bucket", "cabin", "cactus", "camel", "candle",
	"canoe", "canyon", "cargo", "carrot", "castle", "cedar", "chalk", "cherry",
	"cider", "cinder", "clam", "cliff", "clock", "cloud", "clover", "cobalt",
	"comet", "coral", "cotton", "crane", "creek", "crow", "crystal", "cube",
	"daisy", "delta", "denim", "desert", "dingo", "dolphin", "dove", "dragon",
	"drum", "dune", "eagle", "ember", "falcon", "fern", "ferry", "fig",
	"finch", "flame", "flint", "flute", "fog", "forest", "fossil", "fox",
	"frost", "gale", "garnet", "gecko", "ginger", "glacier", "globe", "goose",
	"granite", "grape", "gravel", "gull", "harbor", "hazel", "heron", "hill",
	"honey", "horizon", "husky", "iris", "island", "ivory", "jade", "jaguar",
	"jasper", "jelly", "jungle", "kayak", "kelp", "kettle", "kiwi", "koala",
	"lagoon", "lake", "lantern", "larch", "lava", "lemon", "lily", "lime",
	"linen", "lion", "lotus", "lynx", "magnet", "mango", "maple", "marble",
	"meadow", "melon", "mesa", "mint", "moose", "moss", "moth", "nectar",
	"nest", "nickel", "noodle", "nutmeg", "oak", "oasis", "ocean", "olive",
	"onyx", "opal", "orbit", "orchid", "otter", "owl", "oyster", "paddle",
	"palm", "panda", "papaya", "parrot", "peach", "pearl", "pebble", "pecan",
	"pepper", "piano", "pine", "planet", "plum", "polar", "pony", "poppy",
	"prairie", "puffin", "pumpkin", "quail", "quartz", "quill", "rabbit", "radish",
	"rain", "raven", "reed", "reef", "ribbon", "ridge", "river", "robin",
	"rocket", "rook", "rose", "ruby", "saddle", "saffron", "sage", "salmon",
	"sand", "satin", "scarf", "seal", "sequoia", "shadow", "shell", "silver",
	"sky", "slate", "sloth", "snow", "sparrow", "spruce", "squid", "star",
	"stone", "storm", "sugar", "summit", "sun", "swan", "tango", "thistle",
	"thunder", "tide", "tiger", "timber", "toast", "topaz", "torch", "trout",
	"tulip", "tundra", "turtle", "valley", "velvet", "violet", "volcano", "walnut",
	"walrus", "wave", "willow", "wind", "wolf", "wren", "yak", "zebra",
	"alder", "bluebell", "cocoa", "dahlia", "egret", "fjord", "grove", "hawk",
	"igloo", "juniper", "krill", "lemur", "marsh", "nova", "pixel", "quasar",
}

// Words is the list fingerprints are said in, for the test that the
// phone's copy agrees.
func Words() []string { return append([]string(nil), wordList...) }
