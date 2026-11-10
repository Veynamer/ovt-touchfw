# ovt-touchfw

Инструмент для разбора, проверки и сборки прошивки тачскрина OmniVision/Synaptics
TD4160 и других в формате .img
пример: `X6816DRU_TD4160_TM_fw.img` или `hdl_firmware.img`.

Данная тулза была сделана на основе анализа компонента драйвера Omnivision zeroflash из downstream linux ядра и анализа образа прошивки.
образ прошивки для анализа был взят из:
https://github.com/Veynamer/Infinix-X6816D_dump/blob/X6816D-user-12-SP1A.210812.016-235-release-keys/vendor/firmware/X6816DRU_TD4160_TM_fw.img
и также были проверены эти файлы на чтение:
https://github.com/transsion-mt6789-recovery/twrp-device_infinix_Infinix-X6882/blob/fox_12.1/recovery/root/vendor/firmware/hdl_firmware_01.img
https://github.com/kelexine/twrp_device_tecno_kelly/blob/android-11/recovery/root/vendor/firmware/hdl_firmware.img

Драйвер референс:
https://github.com/rsuntk/android_kernel_samsung_a05m-device_modules/blob/2f3b9c9941d396b93d4830a1c91cc3b694e1d893/drivers/input/touchscreen/TD4160/

## Что умеет

- `verify` — проверить magic, таблицу областей и CRC32 всех областей.
- `list` — показать области, адреса, размеры и CRC.
- `unpack` — разложить образ на области, JSON-метадату, `APP_CONFIG` и manifest.
- `pack` — собрать образ обратно; при неизменных размерах получается
  побайтово идентичный исходному файл.
- `get` — прочитать поле `APP_CONFIG` по имени из JSON-схемы.
- `set` — изменить поле `APP_CONFIG` и собрать новый образ.

`APP_CONFIG` редактируется по схеме из метадаты: все offset/length в схеме
заданы в битах, чтение LSB-first.

## Формат контейнера

Заголовок:

```c
struct image_header {
    u32 magic;              // 0x4818472B
    u32 num_areas;
    u32 offsets[num_areas]; // абсолютные смещения от начала файла
};
```

Дескриптор области — 36 байт, содержимое идёт сразу за ним:

```c
struct area_descriptor {
    u32 magic;              // 0x7C05E516
    char id[16];            // дополняется пробелами
    u32 flags;
    u32 flash_addr_words;   // байтовый адрес = значение * 2
    u32 length;
    u32 checksum;           // CRC32 содержимого
};
```

CRC считается как `crc32(~0, content, length) ^ ~0`, то есть стандартный
`CRC-32/IEEE`. В Go это `crc32.ChecksumIEEE`.

`flags` драйвером из нашего референса не используются. В этом образе у `ROMBOOT_APP_CODE` и
`APP_CONFIG` flags = 1, у остальных 0.

## Области образа

Размер файла: `160307` байт, SHA256:
`3d010ce96d803513e4bb77a870a09f754af5af28e02ff606277be5acda432471`.

| # | ID | descriptor | content | length | flash addr | flags | CRC32 |
|---:|---|---:|---:|---:|---:|---:|---|
| 0 | `BOOT_CONFIG` | `0x00020` | `0x00044` | `0x100` | `0x1F00` | 0 | `0x5E58573C` |
| 1 | `ROMBOOT_APP_CODE` | `0x00144` | `0x00168` | `0x19000` | `0x0000` | 1 | `0x645A29D4` |
| 2 | `APP_CONFIG` | `0x19168` | `0x1918C` | `0x1000` | `0x18000` | 1 | `0x09BB34F7` |
| 3 | `DISPLAY` | `0x1A18C` | `0x1A1B0` | `0x2000` | `0x19000` | 0 | `0xAAB4D2B8` |
| 4 | `OPEN_SHORT` | `0x1C1B0` | `0x1C1D4` | `0x1000` | `0x1B000` | 0 | `0xEA57C40F` |
| 5 | metadata | `0x1D1D4` | `0x1D1DC` | `0xA057` сжатых | — | — | — |

Шестая запись — не `area_descriptor`. Там 8-байтный заголовок:

```text
0xC1FB41D8  u32 compressed_size = 0xA057
```

далее zlib-поток, который разжимается в JSON размером `269614` байт.
Драйвер эту область игнорирует: magic не совпадает с `0x7C05E516`, поэтому
`zeroflash_parse_fw_image()` делает `continue`.

## APP_CONFIG

Содержимое `APP_CONFIG` — блок `SYNA CFG`:

| bit offset | byte | size | поле |
|---:|---:|---:|---|
| 0 | 0 | 64 | `headerID` = 4 × u16 LE = `"SYNA CFG"` |
| 64 | 8 | 32 | `checksum` |
| 96 | 12 | 16 | `length` в 16-битных словах, включая заголовок (`0x05AB`) |
| 112 | 14 | 32 | `buildID` = `0x7D2C06C5` |
| 144 | 18 | 128 | `customerConfigId` |
| 272 | 34 | 21648 | `staticConfig` |
| 21920 | 2740 | 272 | `dynamicConfig` |
| 22192 | 2774 | 1024 | `touchReportDefinition` |

Итого `0x5AB * 2 = 2902` байта, остальное до `0x1000` — padding.

`headerID` в JSON задан как `[22867, 16718, 17184, 18246]`, что в LE u16
и есть `"SYNA CFG"`.

Все offset/length в JSON-схеме — в битах. Поля читаются LSB-first.
Проверено по значениям из реального образа:

- `numColsDoze = 18`
- `numRows = 32`
- `rxCount = 18`
- `txCount = 32`
- `frameRate = 40`
- `gripRedOffset = 133`
- `noiseLengthScale = [80, 80]`

### Внутренний checksum

В JSON написано «Fletcher-32 of the block, starting after the length field»,
то есть от `buildID`. Но стандартный Fletcher-32 по 16/32-битным словам, с
разными start/end/endian/init, не совпал с `0x99106DF8`.

Драйвер внутренний checksum не проверяет — только внешний CRC32 дескриптора.
Поэтому `set` по умолчанию **сохраняет** старый checksum и печатает
предупреждение. Есть режимы:

```bash
touchfw set -checksum=preserve  in.img out.img field=value
touchfw set -checksum=zero      in.img out.img field=value
touchfw set -checksum=fletcher32 in.img out.img field=value
```

`fletcher32` — стандартный вариант; он может не совпадать с vendor-алгоритмом.
Для точного compiler-а нужен эталонный образ, собранный vendor-утилитой, чтобы
восстановить точный вариант.

## Что делает драйвер

`omnivision_tcm_zeroflash.c`:

1. Читает образ, проверяет `0x4818472B`.
2. Идёт по таблице смещений, для каждой области проверяет `0x7C05E516` и CRC32.
3. `BOOT_CONFIG` парсит, но в download-логике не использует. Вероятно, boot
   config уже входит в `ROMBOOT_APP_CODE` (flash `0x1F00` попадает внутрь
   app code `0x0..0x19000`).
4. `ROMBOOT_APP_CODE` скачивает через `CMD_ROMBOOT_DOWNLOAD`: 14
   зарезервированных байт, `buf[0] = size >> 16`, затем данные. Ветка F35
   (`F35_WRITE_FW_TO_PMEM_COMMAND`, control-регистры `+18/+22`) для этого
   образа не используется.
5. `APP_CONFIG` скачивает через `CMD_DOWNLOAD_CONFIG` с 2-байтным префиксом:
   HDL version + `HDL_TOUCH_CONFIG`.
6. `DISPLAY` — аналогично, `HDL_DISPLAY_CONFIG`.
7. `OPEN_SHORT` — аналогично, `HDL_OPEN_SHORT_CONFIG`, но только если
   `fw_status.need_open_short_config`.

`APP_CONFIG` дополняется нулями до кратности 8. Для `0x1000` padding нулевой.

### ньюанс с OPEN_SHORT

В некоторых драйверах Omnivision Touchscreen макрос id для этого параметра
может быть таким и вероятно есть прошивки с использованием такого варианта написания:

```c
#define OPEN_SHORT_ID "OPENSHORT"
```
требует дальнейшего анализа.

## Можно ли декомпилировать код

`ROMBOOT_APP_CODE`:

- первые `0x16` байт похожи на заголовок:
  - `0x00`: `55 AA` — вероятно magic `0xAA55`
  - `0x02`: `01 00` — версия 1
  - `0x04..0x0F`: 12 нулей
  - `0x10`: `32 26` — неизвестно, возможно checksum/version
  - `0x12`: `C5 06 2C 7D` — `buildID = 0x7D2C06C5`
- дальше payload с энтропией ~7.2.
- сигнатур zlib/gzip/lz4/zstd/xz/bz2 нет.
- нет ELF/ARM vector table и читаемых строк.

Вывод: это, скорее всего, шифрованный или проприетарно упакованный код.
Для полноценного дизассемблера/декомпилятора нужны ключ и алгоритм
расшифровки, знание ISA (у TD4160 свой RISC-контроллер) и карта памяти.
Без этого `ROMBOOT_APP_CODE` — просто blob файл, который инструмент
сохраняет как есть.

`BOOT_CONFIG`, `DISPLAY`, `OPEN_SHORT` тоже неизвестно: в JSON-схеме их нет.

## Загрузка, cборка и использование

клонируем репозиторий:
```bash
git clone https://github.com/Veynamer/ovt-touchfw.git
```
сборка:
```bash
cd ovt-touchfw
make build
```

Проверка:

```bash
./touchfw verify `ваш файл`
./touchfw list   `ваш файл`
```

Разбор:

```bash
./touchfw unpack `ваш файл` out
```

В каталоге появятся:

```text
out/
  manifest.json
  header.json
  metadata.json
  appconfig.json
  areas/
    00_BOOT_CONFIG.bin
    01_ROMBOOT_APP_CODE.bin
    02_APP_CONFIG.bin
    03_DISPLAY.bin
    04_OPEN_SHORT.bin
    05_metadata.z
```

Сборка:

```bash
./touchfw pack out new.img
```

Если `metadata.json` не менялся, используется исходный `05_metadata.z`,
поэтому при неизменных областях файл получается побайтово идентичным.
Принудительно пересжать метадату:

```bash
./touchfw pack -recompress out new.img
```

Чтение поля:

```bash
./touchfw get fw.img staticConfiguration.numColsDoze
./touchfw get fw.img staticConfiguration.noiseLengthScale
./touchfw get fw.img configBlock.buildID
```

Изменение поля:

```bash
./touchfw set fw.img new.img staticConfiguration.numColsDoze=20
./touchfw set fw.img new.img staticConfiguration.frameRate=60
./touchfw set fw.img new.img staticConfiguration.noiseLengthScale=90,90
```

Массивы можно задавать через запятую или JSON-массивом:

```bash
./touchfw set fw.img new.img staticConfiguration.rowGain='[256,256,...]'
```

Значения fixed-point (`fractionBits != 0`) задаются в сыром виде, без деления
на `2^fractionBits`.

## Структура проекта

```text
cmd/touchfw/main.go          CLI
internal/touchfw/image.go    контейнер, CRC, metadata, BuildImage
internal/touchfw/appconfig.go SYNA CFG header, checksum strategies
internal/touchfw/schema.go   JSON-схема и битовый codec
internal/touchfw/project.go  unpack/pack/manifest
internal/touchfw/image_test.go
```

## Тесты

```bash
go test ./...
```

Тесты с реальным образом используют путь:

```text
blob/ovt_firmware.img
```

Если файла нет, эти тесты пропускаются. Синтетический тест контейнера
работает всегда.
