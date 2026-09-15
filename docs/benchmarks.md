# Бенчмарки

Каждая запись здесь — это `benchstat`-сравнение для одной конкретной
оптимизации, а не разовая цифра "на глаз". Воспроизвести:

```sh
go test -run=^$ -bench=<Name> -benchmem -count=10 ./... > old.txt
# применить изменение
go test -run=^$ -bench=<Name> -benchmem -count=10 ./... > new.txt
benchstat old.txt new.txt
```

Для end-to-end пропускной способности (PPS/EPS против реально запущенного
collector'а) — `cmd/loadgen`:

```sh
go run ./cmd/loadgen -target 127.0.0.1:2055 -pps 50000 -duration 30s
```

## Журнал

### 2026-09-10 — NetFlow v5 EncodeV5/DecodeV5: ручная упаковка байт вместо reflection в encoding/binary

До: `binary.Write(w, order, header)` / `binary.Read(r, order, &record)` —
каждый аргумент-структура проходит через reflection (`encoder`/`decoder` из
`encoding/binary`), что аллоцирует свежий scratch-буфер на каждый вызов.

После: `binary.BigEndian.PutUint16/32` / `.Uint16/32` пишут напрямую в один
заранее посчитанный по размеру `[]byte` (`putHeader`/`putRecord`/
`getHeader`/`getRecord` в `internal/netflow/{encoderv5,decoderv5}.go`),
поэтому весь пакет строится или разбирается ровно одной аллокацией
независимо от числа записей.

```
                      │   old.txt    │              new.txt                │
                      │    sec/op    │    sec/op     vs base               │
EncodeV5-8              1153.00n ± 3%   45.10n ±  1%  -96.09% (p=0.000 n=10)
EncodeV5_MaxRecords-8   22024.0n ± 5%   498.2n ± 25%  -97.74% (p=0.000 n=10)
DecodeV5-8                635.2n ± 5%   54.72n ±  3%  -91.39% (p=0.000 n=10)
DecodeV5_MaxRecords-8   11866.5n ± 7%   698.3n ±  2%  -94.12% (p=0.000 n=10)
geomean                    3.720µ        171.2n        -95.40%

                      │   old.txt    │              new.txt                │
                      │  allocs/op   │ allocs/op   vs base                 │
EncodeV5-8                4.000 ± 0%   1.000 ± 0%  -75.00% (p=0.000 n=10)
EncodeV5_MaxRecords-8    33.000 ± 0%   1.000 ± 0%  -96.97% (p=0.000 n=10)
DecodeV5-8                4.000 ± 0%   1.000 ± 0%  -75.00% (p=0.000 n=10)
DecodeV5_MaxRecords-8    33.000 ± 0%   1.000 ± 0%  -96.97% (p=0.000 n=10)
geomean                   11.49        1.000       -91.30%
```

Почему: `binary.Write`/`binary.Read` аллоцируют по одному scratch-буферу на
каждый аргумент-структуру через reflection — для пакета с 30 записями это
1 (заголовок) + 30 (записи) = 31 лишняя аллокация поверх выходного буфера,
и всё это на hot path приёма. Ручная упаковка байт через
`encoding/binary.BigEndian` не требует reflection и пишет прямо в уже
выделенный по размеру слайс, поэтому число аллокаций падает до 1
независимо от числа записей (от 1 до 30).

### 2026-09-13 — pipeline.Result.Events: форматирование IPv4 вручную вместо net.IP.String()

Найдено через `pprof` (CPU-профиль снят под реальной нагрузкой
`loadgen -netflow5` на запущенном collector'е, не предположение):
`net.IP.String()` занимал заметный кусок CPU-времени на hot path,
форматируя поля `[4]byte`, которые всегда IPv4 (`netflow.Record.SrcAddr`/
`DstAddr`), никогда не IPv6.

До: `net.IP(rec.SrcAddr[:]).String()` — общая логика определения и
форматирования IPv4/IPv6 для адреса, чья семья уже заранее известна.

После: `formatIPv4`/`appendDecimalByte` (`internal/pipeline/pool.go`) пишут
десятичные цифры прямо в стековый `[15]byte`, одно преобразование
`string()` в конце.

```
                          │ events_old.txt │           events_new.txt           │
                          │     sec/op     │   sec/op     vs base                │
ResultEvents-8                   395.8n ± 1%   376.9n ± 2%  -4.78% (p=0.001 n=10)
ResultEvents_MaxRecords-8         11.27µ ± 4%   10.45µ ± 1%  -7.32% (p=0.000 n=10)
geomean                           2.112µ        1.984µ       -6.06%
```

Почему: пропускает общую dual-family логику `net.IP.String()` — выигрыш
только по CPU-времени. Allocs/op и B/op не изменились (432 B, 8 allocs —
подтверждено `benchstat`, `~ (p=1.000)`): то же самое единственное
преобразование `string()` неизбежно в любом случае. Доминирующая статья
аллокаций на этом пути на самом деле — построение `map[string]any` в той
же функции (~4.4GB из ~6GB, выделенных за профилированный прогон под
нагрузкой) — чтобы это исправить, нужно менять тип `storage.Event.Fields`,
это отдельная, более крупная задача, затрагивающая все бэкенды, здесь не
сделана.

### 2026-09-13 — storage.Event.Fields: упорядоченный []Field вместо map[string]any

Продолжение записи выше — это и есть настоящее исправление той стоимости
аллокаций, которую предыдущая оптимизация не трогала. Подтверждено
изолированным микро-бенчмарком до правки production-кода (построение
`map[string]any` из 7 полей против `[]struct{Key string; Value any}`,
принудительно "убегающих" через package-level sink, чтобы компилятор не
оптимизировал аллокацию прочь): построение map — 2 allocs/op против
1 alloc/op у слайса.

До: `storage.Event.Fields map[string]any` — `internal/pipeline.Result.Events`
строит его как map-литерал на каждую запись NetFlow; каждое нестроковое
значение (`uint16`/`uint8`/`uint32`) упаковывается (boxing) в `any`
отдельно, плюс аллокация самой map (bucket'ы).

После: `storage.Fields []Field` (`Field{Key string; Value any}`,
`internal/storage/storage.go`) — одна аллокация на весь backing-массив.
`Fields.MarshalJSON`/`UnmarshalJSON` сохраняют ту же форму на проводе, что
была у `map[string]any` (плоский JSON-объект), поэтому `JSONExtract` в
ClickHouse и любой Kafka-консьюмер не видят разницы — проверено
round-trip-тестом (`internal/storage/storage_test.go`).

```
                          │ events_new.txt │       events_fields_new.txt        │
                          │     sec/op      │    sec/op     vs base              │
ResultEvents-8                    376.9n ± 2%   237.0n ±  5%  -37.12% (p=0.000 n=10)
ResultEvents_MaxRecords-8        10.449µ ± 1%   7.746µ ± 17%  -25.87% (p=0.002 n=10)
geomean                           1.984µ        1.355µ        -31.73%

                          │ events_new.txt │       events_fields_new.txt        │
                          │      B/op       │     B/op      vs base              │
ResultEvents-8                    432.0 ± 0%     336.0 ± 0%  -22.22% (p=0.000 n=10)
ResultEvents_MaxRecords-8       12.719Ki ± 0%   9.938Ki ± 0%  -21.87% (p=0.000 n=10)
geomean                          2.316Ki        1.806Ki       -22.04%

                          │ events_new.txt │       events_fields_new.txt       │
                          │   allocs/op     │ allocs/op   vs base                │
ResultEvents-8                    8.000 ± 0%   7.000 ± 0%  -12.50% (p=0.000 n=10)
ResultEvents_MaxRecords-8         211.0 ± 0%   181.0 ± 0%  -14.22% (p=0.000 n=10)
geomean                           41.09        35.59       -13.36%
```

Почему: слайсу-литералу нужна одна аллокация на его backing-массив;
map-литералу нужен заголовок хеш-таблицы плюс bucket'ы, вдобавок к тем же
самым значениям, упакованным в `any` в обоих случаях, — собственная
структура map это чистые накладные расходы, которых у слайса нет. `Fields`
остаётся общим для всех протоколов (никакой NetFlow-специфичной структуры,
зашитой в `storage.Event` — IPFIX/Syslog/CEF позже соберут свой `[]Field` с
другими ключами), в отличие от хардкода типизированной структуры, которая
была бы ещё быстрее, но сломала бы этот протокол-агностичный контракт.

Шаблон для каждой новой записи:

```
### <дата> — <что изменилось>

До:      <вывод benchstat>
После:   <вывод benchstat>
Почему:  <одна строка — что дало разницу>
```
