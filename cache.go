package levelcache

const (
	version     int = 1000
	bucketLimit int = 256
)

// Size of a UUID in bytes.
const Size = 16

type UUID [Size]byte

type DevConf struct {
	Name     string
	Dir      string
	Capacity int
}

type Config struct {
	MetaDir        string
	ActionParallel int
	AuxFactory     AuxFactory
}

type Cache struct {
	conf    Config
	meta    *meta
	devices []*device
}

type Auxiliary interface {
	Add(key UUID, auxItem interface{})
	Get(key UUID) interface{}
	Del(key UUID)
	Load(path string)
	Dump(path string)
}

type AuxFactory func(idx int) Auxiliary

type Matcher func(aux Auxiliary) []UUID

func NewCache(conf Config, devices []DevConf) *Cache {
	cache := &Cache{
		conf:    conf,
		meta:    newMeta(conf.MetaDir, conf.AuxFactory),
		devices: make([]*device, len(devices))}

	for lv, devConf := range devices {
		cache.devices[lv] = newDevice(lv, devConf)
	}

	return cache
}

func (c *Cache) Close() {
	for _, d := range c.devices {
		d.close()
	}
}

func (c *Cache) Dump() {
	c.meta.dump(c.conf.ActionParallel)
	for _, d := range c.devices {
		d.dump(c.conf.ActionParallel)
	}
}

func (c *Cache) levelUp(currentLevel int, key UUID, segIndex uint32, data []byte) {
	if currentLevel >= len(c.devices)-1 {
		return
	}

	// TODO, 更复杂的判断逻辑
	d := c.devices[currentLevel+1]
	d.add(key, segIndex, data)
}

func (c *Cache) Get(key UUID, start int, end int) (dataList [][]byte, hitDevs []string, missSegments [][2]int) {
	item := c.meta.get(key)
	if item == nil {
		return nil, nil, nil
	}

	if end == -1 {
		end = int(item.Size)
	}

	startSeg := uint32(start / int(item.SegSize))
	endSeg := uint32(end / int(item.SegSize))

	dataList = make([][]byte, 0)
	missSegments = make([][2]int, 0)
	hitDevs = make([]string, 0)

	for seg := startSeg; seg <= endSeg; seg++ {
		found := false
		for lv := len(c.devices) - 1; lv >= 0; lv-- {
			d := c.devices[lv]
			if tmp := d.get(key, seg); tmp != nil {
				dataList = append(dataList, tmp)
				c.levelUp(lv, key, seg, tmp)
				hitDevs = append(hitDevs, d.conf.Name)
				found = true
				break
			}
		}

		if !found {
			segment := [2]int{
				int(seg * item.SegSize),
				int(seg*item.SegSize + seg)}
			missSegments = append(missSegments, segment)
		}
	}

	return dataList, hitDevs, missSegments
}

func (c *Cache) AddItem(key UUID, expire, size int64, auxData interface{}) {
	const maxSegSize uint32 = 1024 * 1024 * 64
	const minSegSize uint32 = 1024 * 1024
	const defaultSegCount int64 = 1024

	segSize := uint32(size / defaultSegCount)
	if segSize < minSegSize {
		segSize = minSegSize
	}
	if segSize > maxSegSize {
		segSize = maxSegSize
	}

	item := &item{
		Expire:   expire,
		Size:     size,
		SegSize:  segSize,
		Segments: make([]uint32, 0)}

	c.meta.addItem(key, item, auxData)
}

func (c *Cache) AddSegment(key UUID, start int, data []byte) {
	c.meta.addSegment(key, start, start+len(data), func(segIndex uint32) {
		c.devices[0].add(key, segIndex, data)
	})

}

func (c *Cache) Del(k UUID) {
	c.meta.del(k)
	for _, d := range c.devices {
		d.del(k)
	}
}

func (c *Cache) DelBatch(m Matcher) {
	c.meta.delBatch(c.conf.ActionParallel, m)
}
