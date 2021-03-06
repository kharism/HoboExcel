package hoboexcel

import (
	"archive/zip"
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var READ_TEMP_DIR = "./" //dont forget to end it with path separator
var PARTITION_SIZE = 300 //the smaller the faster but it will produce more temporary file

type XlsxRowFetcher struct {
	Filename         string
	ZipFile          *zip.ReadCloser
	Decoder          *xml.Decoder
	CurSheet         io.ReadCloser
	IsUsingRamCache  bool //set this to true if your sharedstring is relatively small
	curPartitionId   int
	cacheSharedStr   []string
	prevRow          int
	prevCol          int
	skipRowCheckNext bool
	skipRowTo        int
	lastToken        xml.Token
}

//seek string with some caching mechanism
func (r *XlsxRowFetcher) SeekString(index int) string {
	fileId := index / PARTITION_SIZE
	if index >= PARTITION_SIZE {
		index = index % PARTITION_SIZE
	}
	if fileId == r.curPartitionId && len(r.cacheSharedStr) > 0 {
		return r.cacheSharedStr[index]
	} else {
		curFile, _ := os.Open(READ_TEMP_DIR + r.Filename + "ss" + strconv.Itoa(fileId))
		defer curFile.Close()
		//curFileStr := READ_TEMP_DIR + r.Filename + "ss" + strconv.Itoa(fileId)
		//fmt.Println(READ_TEMP_DIR + r.Filename + "ss" + strconv.Itoa(fileId))
		decoder := xml.NewDecoder(curFile)
		//curIdx := 0
		tempStr := []string{}
		for {
			tok, _ := decoder.Token()
			if tok == nil {
				break
			}
			switch se := tok.(type) {
			case xml.StartElement:
				if se.Name.Local == "t" {
					tok2, er2 := decoder.Token()
					if er2 != nil {
						fmt.Println(er2.Error())
						os.Exit(-1)
					}
					cd, ok := tok2.(xml.CharData)
					if !ok {
						//fmt.Println("error fetching str data", len(tempStr)+1, "of ", curFileStr)
					}
					//fmt.Println(cd)
					//fmt.Println("%d,%s", preIdx, string(cd))
					tempStr = append(tempStr, string(cd))
				}
			}
		}
		//fmt.Println(tempStr)
		r.cacheSharedStr = tempStr
		r.curPartitionId = fileId
		return r.cacheSharedStr[index]
	}
	return ""
}
func SeekString(filename string, index int) string {
	fileId := index / PARTITION_SIZE
	//preIdx := index
	if index >= PARTITION_SIZE {
		index = index % PARTITION_SIZE
	}
	curFile, _ := os.Open(READ_TEMP_DIR + filename + "ss" + strconv.Itoa(fileId))
	defer curFile.Close()
	decoder := xml.NewDecoder(curFile)
	curIdx := 0
	for {
		tok, _ := decoder.Token()
		if tok == nil {
			break
		}
		switch se := tok.(type) {
		case xml.StartElement:
			if se.Name.Local == "t" {
				if curIdx == index {
					tok2, _ := decoder.Token()
					cd := tok2.(xml.CharData)
					//fmt.Println("%d,%s", preIdx, string(cd))
					return string(cd)
				}
				curIdx++
			}
		default:
			break
		}
	}
	//fmt.Println(preIdx, index)
	return ""
}
func (s *XlsxRowFetcher) Close() error {
	e := s.ZipFile.Close()
	if e != nil {
		return e
	}
	//fmt.Println(TempDir, s.Filename+"ss*")
	sharedStringTemps, _ := filepath.Glob(READ_TEMP_DIR + s.Filename + "ss*")
	for _, f := range sharedStringTemps {
		//fmt.Println("Removing", f)
		os.Remove(f)
	}
	return nil
}

type WriteWorker struct {
	Source       chan string
	CurPartition int
	Filename     string
	TargetBuffer *bufio.Writer
	TargetFile   io.Closer
	WorkerGroup  *sync.WaitGroup
}

func (self *WriteWorker) Run() {
	curCount := 0
	for i := range self.Source {
		self.TargetBuffer.WriteString(i)
		curCount++
		if curCount%PARTITION_SIZE == 0 {
			curCount = 0
			self.TargetBuffer.Flush()
			self.TargetFile.Close()
			self.CurPartition += NUM_WRITER
			newTarget, _ := os.Create(READ_TEMP_DIR + self.Filename + "ss" + strconv.Itoa(self.CurPartition))
			newBuffer := bufio.NewWriter(newTarget)
			self.TargetBuffer = newBuffer
			self.TargetFile = newTarget
		}
	}
	self.TargetBuffer.Flush()
	self.TargetFile.Close()
	self.WorkerGroup.Done()
}

var NUM_WRITER = 2

func PartitionSharedString(filename string) error {
	rr, err := zip.OpenReader(filename)
	baseFilename := filepath.Base(filename)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	defer rr.Close()
	var sharedStrFile *zip.File
	for _, f := range rr.File {
		if strings.Contains(f.Name, "sharedStrings.xml") {
			sharedStrFile = f
			break
		}
	}
	ss, err := sharedStrFile.Open()
	if err != nil {
		return err
	}
	defer ss.Close()
	wg := &sync.WaitGroup{}
	wg.Add(NUM_WRITER)
	var curWorker WriteWorker
	writers := []WriteWorker{}
	for part := 0; part < NUM_WRITER; part++ {
		newWorker := WriteWorker{}
		newWorker.Source = make(chan string, PARTITION_SIZE)
		newWorker.CurPartition = part
		newWorker.Filename = baseFilename
		newWorker.WorkerGroup = wg
		curFile, err := os.Create(READ_TEMP_DIR + baseFilename + "ss" + strconv.Itoa(part))
		if err != nil {
			return err
		}
		newWorker.TargetFile = curFile
		curBuffer := bufio.NewWriter(curFile)
		newWorker.TargetBuffer = curBuffer
		go newWorker.Run()
		writers = append(writers, newWorker)
	}
	curWorker = writers[0]
	idx := 0

	decoder := xml.NewDecoder(ss)
	//counter := 0
	for {
		tok, _ := decoder.Token()
		if tok == nil {
			break
		}

		switch se := tok.(type) {
		case xml.StartElement:
			if se.Name.Local == "t" {
				//fmt.Println(se.Name.Space)
				//counter++
				//fmt.Println(counter)
				val, err := decoder.Token()
				if err != nil {
					fmt.Println(err.Error())
					os.Exit(-1)
				}
				str, ok := val.(xml.CharData)
				if ok {
					//curBuffer.WriteString("<t>" + string(str) + "</t>")
					curWorker.Source <- "<t>" + html.EscapeString(string(str)) + "</t>"
					idx++
					//fmt.Println(html.EscapeString(string(str)))
					if idx%PARTITION_SIZE == 0 {
						curPartition := idx / PARTITION_SIZE
						curWorker = writers[curPartition%NUM_WRITER]
						// curBuffer.Flush()
						// curFile.Close()
						// curFile, err = os.Create(READ_TEMP_DIR + baseFilename + "ss" + fmt.Sprintf("%d", idx/PARTITION_SIZE))
						// if err != nil {
						// 	return err
						// }
						// curBuffer = bufio.NewWriter(curFile)
					}
				} else {
					ee, ok := val.(xml.EndElement)
					if ok && ee.Name.Local == "t" {
						curWorker.Source <- "<t></t>"
						idx++
						if idx%PARTITION_SIZE == 0 {
							curPartition := idx / PARTITION_SIZE
							curWorker = writers[curPartition%NUM_WRITER]
						}
					}

				}

			}
			break
		default:
			break
		}
	}
	for _, c := range writers {
		close(c.Source)
	}
	wg.Wait()
	return nil
}

type Column struct {
	IsString bool
	CellCode string
	val      string
}

func Power(base, power int) int {
	if power == 0 {
		return 1
	}
	hasil := 1
	for i := 1; i <= power; i++ {
		hasil *= base
	}
	return hasil
}
func GetExcelColumnName(columnNumber int) string {
	dividend := columnNumber
	columnName := ""
	modulo := byte(0)
	for {

		modulo = byte((dividend - 1) % 26)
		//fmt.Println("SSS", string([]byte{65 + modulo}))
		columnName = string([]byte{65 + modulo}) + columnName
		dividend = (int)((dividend - int(modulo)) / 26)
		if dividend == 0 {
			break
		}
	}

	return columnName
}
func getColIndex(source string) int {
	sourceLower := strings.ToLower(source)
	//colId := ""
	colNum := 0
	ff := []int{}
	for _, c := range sourceLower {
		if c >= 48 && c <= 57 {
			//colId=source[:idx]
			break
		} else {
			//colNum+=int(c)-96
			ff = append(ff, int(c)-96)
		}
	}
	//fmt.Println(ff)
	for idx, c := range ff {
		colNum += c * Power(26, len(ff)-idx-1)
	}
	return colNum
}

type ColFetcher struct {
	Decoder          *xml.Decoder
	CurSheet         io.ReadCloser
	IsUsingRamCache  bool //set this to true if your sharedstring is relatively small
	curPartitionId   int
	cacheSharedStr   []string
	prevRow          int
	prevCol          int
	skipRowCheckNext bool
	skipRowTo        int
	lastToken        xml.Token
}

func (self *XlsxRowFetcher) NextRow() []string {
	// logger := log.New(os.Stdout, "NEXTROW", log.Llongfile)
	if self.skipRowTo != 0 && self.skipRowTo > self.prevRow+1 {
		self.prevRow++
		return []string{}
	}
	for {
		var tok xml.Token
		if self.lastToken == nil {
			tok, _ = self.Decoder.Token()
			if tok == nil {
				return nil
			}
		} else {
			tok = self.lastToken
		}
		//logger.Println("TOK",tok)
		switch se := tok.(type) {
		case xml.StartElement:
			if se.Name.Local == "row" {
				//fmt.Println("New Row")
				curRow := 0
				for _, tagAttr := range se.Attr {
					if tagAttr.Name.Local == "r" {
						curRow, _ = strconv.Atoi(tagAttr.Value)
					}
				}
				if curRow > self.prevRow+1 {
					self.prevRow++
					self.skipRowTo = curRow
					self.lastToken = tok
					return []string{}
				}
				self.skipRowTo = 0
				self.lastToken = nil
				self.prevCol = 0
				curCol := 0
				cols := []Column{}
				colCount := 0
			columnLoop:
				for {
					s, _ := self.Decoder.Token()
					//logger.Println("Fetching Token",s)
					if cc, ok := s.(xml.StartElement); ok {
						cellId := ""
						if cc.Name.Local == "c" {
							//fmt.Println("Col", cc.Attr)
							isString := false
							for _, kk := range cc.Attr {
								if kk.Name.Local == "t" && kk.Value == "s" {
									isString = true
								}
								if kk.Name.Local == "r" {
									curCol = getColIndex(kk.Value)
									// logger.Println("Processing Cell", kk.Value, curCol)
									cellId = kk.Value
								}
							}
							if curCol > self.prevCol+1 {
								// logger.Println("Appending emptyCollumn", curRow, curCol, GetExcelColumnName(curCol), self.prevCol+1, GetExcelColumnName(self.prevCol+1))
								for kk := self.prevCol; kk < curCol-1; kk++ {
									cols = append(cols, Column{false, "DummySh", ""})
									colCount++
								}
								// logger.Println("Done Appending emptyCollumn", GetExcelColumnName(curCol), curCol, len(cols))
							}
							self.prevCol = curCol
							//look for next valid token either v or c

							for {
								ss, _ := self.Decoder.Token()
								//logger.Println(ss)
								if cc2, ok := ss.(xml.StartElement); ok {
									if cc2.Name.Local == "v" {
										cont, _ := self.Decoder.Token()
										if cd, ok := cont.(xml.CharData); ok {
											if isString {
												//fmt.Println("CharData String", string(cd))
												cols = append(cols, Column{true, cellId, string(cd)})
												colCount++
											} else {
												//fmt.Println("CharData", string(cd))
												cols = append(cols, Column{false, cellId, string(cd)})
												colCount++
											}
											// logger.Println("Setting value on", cellId, isString)
											//self.prevCol = curCol
										}
										//logger.Println("Breaking shits")
										continue columnLoop
									}
								}
								if cc2, ok := ss.(xml.EndElement); ok {
									if cc2.Name.Local == "c" {
										// logger.Println("adding empty column on", GetExcelColumnName(curCol), cellId)
										cols = append(cols, Column{false, cellId, ""})
										// logger.Println("Breaking shits")
										continue columnLoop
									}
								}
							}
						}
					}
					if cc, ok := s.(xml.EndElement); ok {
						if cc.Name.Local == "row" {
							// logger.Println("Done looping row")
							strCols := []string{}
							for _, c := range cols {
								if c.IsString {
									idx, _ := strconv.Atoi(c.val)
									//fmt.Println(idx)
									if self.IsUsingRamCache {
										// logger.Println("Seeking String", idx)
										c.val = self.SeekString(idx)
									} else {
										c.val = SeekString(self.Filename, idx)
									}

								}
								// logger.Println("Appending RealValue", c.CellCode, c.val, c.IsString)
								strCols = append(strCols, c.val)
							}
							//fmt.Println(strCols)
							//break
							self.prevRow = curRow
							return strCols
						}
					}
				}
			}
		}
	}
	return nil
}

var SheetNotFoundError = errors.New("Sheet Not Found")

func GetSheetId(file io.Reader, sheetTarget string) string {
	decoder := xml.NewDecoder(file)
	for {
		tok, err := decoder.Token()
		if err != nil {
			return ""
		}
		if cc, ok := tok.(xml.StartElement); ok {
			switch cc.Name.Local {
			case "sheet":
				correctSheet := false
				for _, a := range cc.Attr {
					if a.Name.Local == "name" && a.Value == sheetTarget {
						correctSheet = true
						//break
					}
					if correctSheet {
						if a.Name.Local == "id" {
							//fmt.Println("sheet" + a.Value[3:])
							return "sheet" + a.Value[3:]
						}
					}
				}

				break
			}
		}
	}
	return ""
}
func Import(filename string, sheetname string) (*XlsxRowFetcher, error) {
	res := &XlsxRowFetcher{}
	res.Filename = filepath.Base(filename)
	xlsxFile, err := zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}
	//lookup sheet id
	var xlWorkbook *zip.File
	for _, f := range xlsxFile.File {
		if strings.HasSuffix(f.Name, "xl/workbook.xml") {
			xlWorkbook = f
			break
		}
	}
	ff, _ := xlWorkbook.Open()
	sheetname = GetSheetId(ff, sheetname)
	ff.Close()
	res.ZipFile = xlsxFile
	var curSheet *zip.File
	for _, f := range xlsxFile.File {
		if strings.HasSuffix(f.Name, sheetname+".xml") {
			curSheet = f
			break
		}
	}
	if curSheet == nil {
		return nil, SheetNotFoundError
	}
	file, err := curSheet.Open()
	if err != nil {
		return nil, err
	}
	//defer file.Close()
	res.CurSheet = file
	decoder := xml.NewDecoder(file)
	res.Decoder = decoder
	PartitionSharedString(filename)
	return res, nil
}
