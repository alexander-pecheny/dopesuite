// wordlist.js — the vocabulary for xkcd-style ("correct-horse-battery-staple")
// generated passphrases. Data only, no logic: short, common, easy-to-type,
// unambiguous English words (no homophone-heavy or offensive entries), so a
// generated board passphrase stays memorable and painless to dictate to a
// co-author. crypto.js#generatePassphrase draws from it uniformly.
//
// Entropy is `words × log2(len)`; at 248 words that's ~7.95 bits/word, so the
// default 6-word passphrase carries ~48 bits — well above what a human picks,
// and behind scrypt each guess is already expensive.
export const WORDLIST = [
  // animals
  "ant", "badger", "bat", "bear", "beaver", "bison", "camel", "cobra",
  "crab", "crane", "deer", "dolphin", "dove", "dragon", "duck", "eagle",
  "falcon", "ferret", "finch", "fox", "frog", "gecko", "goat", "goose",
  "hawk", "heron", "horse", "koala", "lark", "lion", "llama", "lynx",
  "magpie", "mole", "moose", "mouse", "newt", "otter", "owl", "panda",
  "panther", "parrot", "penguin", "pigeon", "pony", "puffin", "puma", "rabbit",
  "raccoon", "raven", "robin", "salmon", "seal", "shark", "sheep", "snail",
  "snake", "sparrow", "spider", "squid", "stork", "swan", "tiger", "turtle",
  "walrus", "weasel",
  // nature
  "acorn", "amber", "beach", "birch", "bloom", "boulder", "branch", "breeze",
  "brook", "canyon", "cave", "cedar", "cliff", "cloud", "clover", "comet",
  "coral", "cove", "creek", "crystal", "dawn", "delta", "desert", "dew",
  "dune", "dusk", "ember", "fern", "flame", "forest", "fossil", "frost",
  "galaxy", "garden", "geyser", "glacier", "glade", "granite", "grove", "hollow",
  "island", "jungle", "lagoon", "lake", "leaf", "lily", "maple", "marsh",
  "meadow", "meteor", "mist", "moss", "mountain", "nebula", "oasis", "ocean",
  "orchard", "pebble", "petal", "pine", "planet", "pond", "prairie", "rainbow",
  "ravine", "reef", "ridge", "river", "sequoia", "shadow", "shore", "spruce",
  "star", "stone", "storm", "stream", "summit", "sunset", "swamp", "thicket",
  "thunder", "tide", "timber", "trail", "tundra", "valley", "volcano", "wave",
  "willow", "wind", "woodland",
  // objects
  "anchor", "anvil", "arrow", "banner", "barrel", "basket", "beacon", "bell",
  "bottle", "bridge", "bucket", "candle", "cannon", "castle", "chariot", "cloak",
  "compass", "cottage", "crown", "dagger", "engine", "feather", "hammer", "helmet",
  "kettle", "ladder", "lantern", "marble", "mirror", "needle", "paddle", "pillar",
  "quill", "ribbon", "saddle", "scroll", "shield", "signal", "temple", "thimble",
  "torch", "tower", "wagon", "window",
  // food & plants
  "almond", "apple", "apricot", "barley", "basil", "berry", "cherry", "cider",
  "cocoa", "ginger", "honey", "lemon", "mango", "melon", "mint", "nutmeg",
  "olive", "onion", "orange", "peach", "peanut", "pear", "pepper", "plum",
  "pumpkin", "raisin", "saffron", "sage", "sugar", "thyme", "tomato", "turnip",
  "vanilla", "walnut", "wheat",
  // colors
  "azure", "cobalt", "copper", "crimson", "golden", "indigo", "ivory", "jade",
  "pearl", "scarlet", "silver", "violet",
];
