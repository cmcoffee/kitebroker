# cfg
--
    import "github.com/cmcoffee/go-snuglib/cfg"

Package 'cfg' provides functions for reading and writing configuration files and
their coresponding string values.

    Ignores '#' as comments, ','s denote multiple values.

    # Example config file.
    [section]
    key = value
    key2 = value1, value2
    key3 = value1,
           value2,
           value3

    [section2]
    key = value1,
          value2,
          value3

## Usage

#### type Store

```go
type Store struct {
}
```


#### func (*Store) Defaults

```go
func (s *Store) Defaults(input string) (err error)
```
Sets default settings for configuration store, ignores if already set.

#### func (*Store) Exists

```go
func (s *Store) Exists(input ...string) (found bool)
```
Returns true if section or section and key exists.

#### func (*Store) File

```go
func (s *Store) File(file string) (err error)
```
Reads configuration file and returns Store, file must exist even if empty.

#### func (*Store) Get

```go
func (s *Store) Get(section, key string) string
```
Return only the first entry, if there are multiple entries the rest are skipped.

#### func (*Store) GetBool

```go
func (s *Store) GetBool(section, key string) (output bool)
```
Get Boolean Value from config.

#### func (*Store) GetFloat

```go
func (s *Store) GetFloat(section, key string) (output float64)
```
Get Float64 Value from config.

#### func (*Store) GetInt

```go
func (s *Store) GetInt(section, key string) (output int64)
```
Get Int64 Value from config.

#### func (*Store) GetUint

```go
func (s *Store) GetUint(section, key string) (output uint64)
```
Get UInt64 Value from config.

#### func (*Store) Keys

```go
func (s *Store) Keys(section string) (out []string)
```
Returns keys of section specified.

#### func (*Store) MGet

```go
func (s *Store) MGet(section, key string) []string
```
Returns array of all retrieved string values under section with key.

#### func (*Store) Parse

```go
func (s *Store) Parse(input string) (err error)
```
Will parse a string, but overwrite existing config.

#### func (*Store) SGet

```go
func (s *Store) SGet(section, key string) string
```
Returns entire line as one string, (Single Get)

#### func (*Store) Sanitize

```go
func (s *Store) Sanitize(section string, keys []string) (err error)
```
Goes through list of sections and keys to make sure they are set.

#### func (*Store) Save

```go
func (s *Store) Save(sections ...string) error
```
Saves [section](s) to file, recording all key = value pairs, if empty, save all
sections.

#### func (*Store) Sections

```go
func (s *Store) Sections() (out []string)
```
Returns array of all sections in config file.

#### func (*Store) Set

```go
func (s *Store) Set(section, key string, value ...interface{}) (err error)
```
Sets key = values under [section], updates Store and saves to file.

#### func (*Store) TrimSave

```go
func (s *Store) TrimSave(sections ...string) error
```
TrimSave is similar to Save, however it will trim unusued keys.

#### func (*Store) Unset

```go
func (s *Store) Unset(input ...string)
```
Unsets a specified key, or specified section. If section is empty, section is
removed.
