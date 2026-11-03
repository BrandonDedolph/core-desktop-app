package app

import (
	"hash/fnv"
	"strings"
	"time"
)

// The canned Music views: the demo grid's albums with an artist, a genre and
// a tracklist each, so the Artists, Genres and Songs views have something to
// be reviewed against with no files on the machine. The titles are the
// records' own where they are written out here; the rest are numbered.

// demoGenres is each canned album's genre, by its artist.
var demoGenres = map[string]string{
	"Talking Heads": "New Wave", "Steely Dan": "Jazz Rock", "Stevie Wonder": "Soul",
	"Talk Talk": "Art Rock", "Kate Bush": "Art Pop", "Ruel": "Pop", "ericdoa": "Hyperpop",
	"Arizona Zervas": "Hip-Hop", "Morgan Wallen": "Country", "Fleetwood Mac": "Rock",
	"Radiohead": "Alternative", "D'Angelo": "R&B", "Frank Ocean": "R&B", "Nick Drake": "Folk",
	"Little Simz": "Hip-Hop", "Sade": "R&B", "Björk": "Electronic", "Portishead": "Trip-Hop",
	"Cameron Dallas": "Pop", "Beach House": "Dream Pop", "Burial": "Electronic",
	"The Blue Nile": "Sophisti-Pop", "Arthur Russell": "Experimental", "Solange": "R&B",
}

// demoTitles are the tracklists written out, by album.
var demoTitles = map[string][]string{
	"Aja": {"Black Cow", "Aja", "Deacon Blues", "Peg", "Home at Last", "I Got the News", "Josie"},
	"Innervisions": {"Too High", "Visions", "Living for the City", "Golden Lady", "Higher Ground",
		"Jesus Children of America", "All in Love Is Fair", "Don't You Worry 'bout a Thing",
		"He's Misstra Know-It-All"},
	"Spirit of Eden": {"The Rainbow", "Eden", "Desire", "Inheritance", "I Believe in You", "Wealth"},
	"Hounds of Love": {"Running Up That Hill", "Hounds of Love", "The Big Sky", "Mother Stands for Comfort",
		"Cloudbusting", "And Dream of Sheep", "Under Ice", "Waking the Witch", "Watching You Without Me",
		"Jig of Life", "Hello Earth", "The Morning Fog"},
	"Rumours": {"Second Hand News", "Dreams", "Never Going Back Again", "Don't Stop", "Go Your Own Way",
		"Songbird", "The Chain", "You Make Loving Fun", "I Don't Want to Know", "Oh Daddy", "Gold Dust Woman"},
	"In Rainbows": {"15 Step", "Bodysnatchers", "Nude", "Weird Fishes/Arpeggi", "All I Need", "Faust Arp",
		"Reckoner", "House of Cards", "Jigsaw Falling into Place", "Videotape"},
	"Pink Moon": {"Pink Moon", "Place to Be", "Road", "Which Will", "Horn", "Things Behind the Sun", "Know",
		"Parasite", "Free Ride", "Harvest Breed", "From the Morning"},
	"Dummy": {"Mysterons", "Sour Times", "Strangers", "It Could Be Sweet", "Wandering Star", "It's a Fire",
		"Numb", "Roads", "Pedestal", "Biscuit", "Glory Box"},
}

// demoSongs fills a canned album's Artist and Songs: its tracklist when it
// carries one, else the titles above, else numbered tracks; the first Copy
// songs carry the album's state, the rest are on the iPod.
func demoSongs(a *Album) {
	artist, album := albumArtist(*a), tileTitle(*a)
	a.Artist = artist
	genre := demoGenres[artist]
	a.Songs = make([]Song, a.Tracks)
	for i := range a.Songs {
		s := Song{Artist: artist, Genre: genre, Disc: 1, Number: i + 1, Size: 31_500_000}
		switch {
		case i < len(a.TrackList):
			s.Title, s.Length = a.TrackList[i].Title, a.TrackList[i].Length
		case i < len(demoTitles[album]):
			s.Title = demoTitles[album][i]
		default:
			s.Title = "Track " + itoa(i+1)
		}
		if s.Length == 0 {
			h := fnv.New32a()
			_, _ = h.Write([]byte(album + s.Title))
			s.Length = time.Duration(170+h.Sum32()%190) * time.Second
		}
		if i < a.Copy {
			s.State = a.State
		}
		a.Songs[i] = s
	}
}

// ArtistsState is the demo on the Library tab's Artists view.
func ArtistsState() State { st := DemoState(); st.View = ViewArtists; return st }

// GenresState is the demo on the Genres view.
func GenresState() State { st := DemoState(); st.View = ViewGenres; return st }

// GenreState is one genre open: the grid of its albums, the switch reading
// "‹ Genres › Hip-Hop".
func GenreState() State { st := GenresState(); st.Genre = "Hip-Hop"; return st }

// SongsState is the demo on the Songs view, Remain in Light's third track
// playing so the table's playing row is in the picture.
func SongsState() State {
	st := DemoState()
	st.View = ViewSongs
	setDemoPlaying(&st)
	return st
}

// demoArtist is the artist `--dialog artist` opens: the first with more
// than one album (a real library's, under --source), else the artist of the
// album the canned sheet opens, else the first.
func demoArtist(st *State) string {
	cat := BuildCatalog(st.Albums)
	for _, a := range cat.Artists {
		if len(a.Albums) > 1 {
			return a.Name
		}
	}
	for _, a := range st.Albums {
		if len(a.TrackList) > 0 {
			return albumArtist(a)
		}
	}
	if len(cat.Artists) > 0 {
		return cat.Artists[0].Name
	}
	return ""
}

// ViewByName is a --view word, for the screenshot flag.
func ViewByName(s string) (MusicView, bool) {
	for _, v := range musicViews {
		if strings.EqualFold(s, viewKey(v)) {
			return v, true
		}
	}
	return 0, false
}
