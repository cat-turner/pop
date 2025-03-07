package supply

import (
	"math"

	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
)

// Regions: This is very experimental and needs more iterations to be stable

// RegionCode defines a subnetwork code
type RegionCode uint64

const (
	// GlobalRegion region is a free global network for anyone to try the network
	GlobalRegion RegionCode = iota
	// AsiaRegion is a specific region to connect caches in the Asian area
	AsiaRegion
	// AfricaRegion is a specific region in the African geographic area
	AfricaRegion
	// SouthAmericaRegion is a specific region
	SouthAmericaRegion
	// NorthAmericaRegion is a specific region to connect caches in the North American area
	NorthAmericaRegion
	// EuropeRegion is a specific region to connect caches in the European area
	EuropeRegion
	// OceaniaRegion is a specific region
	OceaniaRegion
	// CustomRegion is a user defined region
	CustomRegion = math.MaxUint64
)

// Region represents a CDN subnetwork.
type Region struct {
	// The official region name should be unique to avoid clashing with other regions.
	Name string
	// Code is a compressed identifier for the region.
	Code RegionCode
	// PPB is the minimum price per byte in FIL defined for this region. This does not account for
	// any dynamic pricing mechanisms.
	PPB abi.TokenAmount
	// StorageMiners is a list of known storage miner ids in this region. We plan
	// to enable a better way to select new miners (maybe Textile API?) but for now we hard code an initial list.
	StorageMiners []string
}

var (
	asia = Region{
		Name: "Asia",
		Code: AsiaRegion,
		PPB:  abi.NewTokenAmount(1),
		StorageMiners: []string{
			"f0159961", // (China)
			"f0242152", // (Korea)
			"f0225676", // (Korea)
			"f0165539", // (Japan)
			"f0216138", // (China)
			"f01272",   // StorSwift (China)
		},
	}
	africa = Region{
		Name: "Africa",
		Code: AfricaRegion,
		PPB:  abi.NewTokenAmount(1),
	}
	southAmerica = Region{
		Name: "SouthAmerica",
		Code: SouthAmericaRegion,
		PPB:  abi.NewTokenAmount(1),
	}
	northAmerica = Region{
		Name: "NorthAmerica",
		Code: NorthAmericaRegion,
		PPB:  abi.NewTokenAmount(1),
		// NorthAmerica region based miners in order of most capacity to least
		StorageMiners: []string{
			"f02301",   // topblocks
			"f03223",   // telos - crazy PPB not sure if error or not
			"f019104",  // Filswan (NBFS) Canada
			"f02401",   // Terra Mining
			"f02387",   // W3Bcloud
			"f09848",   // BigBearLake
			"f08399",   // MiningMusing
			"f010088",  // Purumine
			"f064218",  // Simba
			"f02540",   // Foundry
			"f015927",  // CDImine
			"f0135078", // Adept
			"f019104",  // NorthStar
			"f047419",  // Citadel
			"f01278",   // MiMiner
			"f010617",  // kernelogic2
			"f01247",   // BigChungus
			"f022142",  // Nelson SR2
			"f019279",  // Pd2
		},
	}
	europe = Region{
		Name: "Europe",
		Code: EuropeRegion,
		PPB:  abi.NewTokenAmount(1),
		StorageMiners: []string{
			"f01240",  // Dcent (Netherlands)
			"f01234",  // Eliovp (Belgium)
			"f022352", // TechHedge (Norway)
			"f099608", // stander (Latvia)
			"f02576",  // BenjaminH
			"f023467", // PhiMining (Norway)
			"f03624",  // ode (Germany)
			"f062353", // P (Germany)
			"f081323", // Midland UK
			"f08403",  // TippyFlits (UK)
			"f010446", // (Belgium)
			"f01277",  // tvsthlm (Sweden) crazy PPB
			"f022163", // (Switzerland)
		},
	}
	oceania = Region{
		Name: "Oceania",
		Code: OceaniaRegion,
		PPB:  abi.NewTokenAmount(1),
		StorageMiners: []string{
			"f014365", // 🥭
		},
	}
	global = Region{
		Name: "Global",
		Code: GlobalRegion,
		PPB:  big.Zero(),
		// Global takes all the miners
		StorageMiners: append(
			northAmerica.StorageMiners,
			append(europe.StorageMiners, oceania.StorageMiners...)...,
		),
	}
)

// Regions is a list of preset regions
var Regions = map[string]Region{
	"Global":       global,
	"Asia":         asia,
	"Africa":       africa,
	"SouthAmerica": southAmerica,
	"NorthAmerica": northAmerica,
	"Europe":       europe,
	"Oceania":      oceania,
}

// ParseRegions converts region names to region structs
func ParseRegions(list []string) []Region {
	var regions []Region
	for _, rstring := range list {
		if r := Regions[rstring]; r.Name != "" {
			regions = append(regions, r)
			continue
		}
		// We also support custom regions if users want their own provider subnet
		regions = append(regions, Region{
			Name: rstring,
			Code: CustomRegion,
		})
	}
	return regions
}
