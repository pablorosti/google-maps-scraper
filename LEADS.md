# Guía de leads por provincia

`leads.sh` obtiene leads de Google Maps para una provincia argentina completa, rubro por rubro, y guarda un archivo por rubro listo para abrir en Excel. Corre en Docker, retoma solo después de cualquier corte y no necesita intervención hasta llegar al total pedido.

## Inicio rápido

1. Abrir Docker Desktop.
2. Desde la carpeta del repositorio, lanzar la provincia y el total de leads:
   ```bash
   ./leads.sh iniciar cordoba 50000
   ```
3. Revisar el avance cuando se quiera:
   ```bash
   ./leads.sh estado cordoba
   ```
4. Los archivos quedan en `leads-output/cordoba/<rubro>.csv`. Cuando `estado` muestra `Estado: TERMINADO`, el trabajo está completo y se puede apagar con `./leads.sh parar cordoba`.

> La primera ejecución tarda unos minutos más porque construye la imagen de Docker.

## Preparar la computadora (una sola vez)

Para que el scrapeo no se corte durante días:

| Qué | Cómo |
|-----|------|
| Docker arranca solo | Docker Desktop → Settings → General → activar **"Start Docker Desktop when you sign in"** |
| La computadora no se duerme | Mac: automático (ver abajo). Windows: configuración manual (ver abajo) |
| Espacio en disco | Calcular ~9 KB por lead en los datos internos: unos 4 GB cada 400.000 leads |

### Mac

`iniciar` y `reanudar` mantienen la Mac despierta mientras corre el scrapeo (`caffeinate`), y `parar` lo desactiva. Después de reiniciar la Mac, ejecutar `./leads.sh reanudar <provincia>` para reactivarlo.

### Windows

En Windows, `leads.sh` no mantiene la PC despierta por sí solo. Hay que configurarlo una vez con cualquiera de estas opciones.

**Opción A: PowerToys Awake (la más simple).** Instalar [Microsoft PowerToys](https://learn.microsoft.com/windows/powertoys/), abrir **Awake** y elegir **"Keep awake indefinitely"**. Funciona igual que `caffeinate`: se activa y desactiva desde el ícono de la bandeja del sistema.

**Opción B: desactivar la suspensión con `powercfg`.** En una terminal de PowerShell o CMD como administrador:

```powershell
# No suspender ni hibernar mientras está enchufada (0 = nunca)
powercfg /change standby-timeout-ac 0
powercfg /change hibernate-timeout-ac 0

# Notebook: no suspender al cerrar la tapa estando enchufada
powercfg /setacvalueindex SCHEME_CURRENT SUB_BUTTONS LIDACTION 0
powercfg /setactive SCHEME_CURRENT
```

Para volver a la configuración habitual al terminar (por ejemplo, suspender a los 30 minutos):

```powershell
powercfg /change standby-timeout-ac 30
powercfg /change hibernate-timeout-ac 60
powercfg /setacvalueindex SCHEME_CURRENT SUB_BUTTONS LIDACTION 1
powercfg /setactive SCHEME_CURRENT
```

**Opción C: desde la configuración.** Configuración → Sistema → Energía y batería → Pantalla y suspensión → "Cuando esté enchufado, poner el dispositivo en suspensión después de" → **Nunca**.

Además, en Windows conviene tener en cuenta:

- **Windows Update reinicia la PC.** Pausar las actualizaciones mientras dura el scrapeo (Configuración → Windows Update → Pausar actualizaciones). Si igual se reinicia, Docker Desktop vuelve a levantar el contenedor y el scrapeo retoma solo, siempre que Docker esté configurado para arrancar al iniciar sesión.
- **Dónde ejecutar `leads.sh`:** desde una terminal de WSL (Ubuntu) o Git Bash, con Docker Desktop usando WSL 2. Los comandos son los mismos que en Mac.
- **Batería:** en una notebook, dejarla siempre enchufada. Las opciones anteriores solo evitan la suspensión con el cargador conectado.

## Comandos

| Comando | Para qué sirve |
|---------|----------------|
| `./leads.sh iniciar <provincia> <total>` | Arranca (o continúa) una provincia, repartiendo el total entre todos los rubros |
| `./leads.sh iniciar <provincia> <total> <por_rubro>` | Llena los rubros en orden, hasta `<por_rubro>` cada uno |
| `./leads.sh estado <provincia>` | Muestra leads por rubro y el estado del contenedor |
| `./leads.sh logs <provincia>` | Muestra en vivo lo que hace. `Ctrl+C` sale de los logs sin frenar el scrapeo |
| `./leads.sh parar <provincia>` | Detiene el scrapeo. El progreso queda guardado |
| `./leads.sh reanudar <provincia>` | Continúa con la misma configuración guardada (no hace falta repetir el total) |
| `./leads.sh reiniciar <provincia>` | Reinicia el contenedor |
| `./leads.sh reparar <provincia>` | Reconstruye la imagen desde cero y recrea el contenedor. Los leads no se tocan |
| `./leads.sh exportar <provincia>` | Regenera los archivos por rubro desde los datos ya scrapeados, sin scrapear |
| `./leads.sh diagnostico <provincia>` | Revisa Docker, contenedor, último avance, disco, proxies y avisos recientes |
| `./leads.sh rubros` | Lista los rubros disponibles |

Los nombres con espacios van entre comillas: `./leads.sh iniciar "buenos aires" 200000`.

## Cómo reparte los leads

**Sin `por_rubro` (recomendado):** el total se reparte en partes iguales entre los 39 rubros. Si un rubro chico se queda sin negocios (por ejemplo, comercio exterior), conserva lo que encontró y su parte sobrante pasa a los rubros que todavía tienen negocios. Termina al llegar al total, o cuando la provincia ya no tiene más negocios para buscar.

**Con `por_rubro`:** llena los rubros en el orden del catálogo, cada uno hasta ese tope. Con un total chico, solo se llenan los primeros rubros.

**Dentro de cada rubro**, busca en este orden:

1. Cada término del rubro en cada localidad oficial de la provincia (por ejemplo, "pizzerías en Villa María, Córdoba, Argentina").
2. Una grilla de 1 km sobre las ciudades grandes, donde una sola búsqueda de Google se corta en ~120 resultados.

| Provincia | Ciudades con grilla |
|-----------|---------------------|
| Buenos Aires | CABA, conurbano norte, oeste y sur, La Plata, Mar del Plata, Bahía Blanca, Tandil, Luján, Zárate, Junín, Pergamino, Olavarría |
| CABA | Toda la ciudad |
| Córdoba | Córdoba, Río Cuarto, Villa María, San Francisco, Villa Carlos Paz, Alta Gracia, Río Tercero |
| Santa Fe | Rosario, Santa Fe |
| Mendoza | Mendoza |
| Tucumán | San Miguel de Tucumán |

El resto de las provincias se busca solo por localidad.

### Buenos Aires y CABA

- `buenos aires` cubre **toda la provincia más CABA**, en los mismos archivos. La columna `provincia` indica de dónde es cada lead.
- `caba` (o `"capital federal"`, `"ciudad autonoma de buenos aires"`) scrapea solo CABA, en `leads-output/caba/`.

## Qué pasa si algo falla

La mayoría de los problemas se resuelven solos:

| Situación | Qué hace solo |
|-----------|---------------|
| Se cae el scraper | Lo reintenta |
| Se corta internet o Google bloquea | Pausa 10 minutos, duplicando la pausa hasta 2 horas, y reintenta. Al volver los resultados, la pausa vuelve a 10 minutos |
| El scraper queda trabado 20 minutos sin resultados | Lo reinicia |
| Uso de memoria de Chrome | Recicla el scraper cada 3 horas |
| Se cae el contenedor o se reinicia Docker | Docker lo vuelve a levantar y retoma desde el estado guardado |

Si `estado` no avanza, seguir esta escalera:

1. `./leads.sh diagnostico <provincia>`: indica qué pasa y qué comando usar.
2. `./leads.sh reanudar <provincia>`: si el contenedor no existe o está detenido.
3. `./leads.sh reiniciar <provincia>`: si el contenedor corre pero no avanza.
4. `./leads.sh reparar <provincia>`: si sigue igual después de reiniciar.

Ninguno de estos comandos borra leads.

## Archivos de salida

```
leads-output/
├── proxies.txt                 # opcional, ver "Proxies"
└── cordoba/
    ├── gastronomia.csv         # un archivo por rubro
    ├── kioscos-y-almacenes.csv
    ├── ...
    ├── PROGRESO.txt            # resumen del avance
    ├── .raw/                   # datos crudos del scraper: NO borrar
    └── .state/                 # estado para retomar: NO borrar
```

Los CSV usan `;` como separador y codificación UTF-8 con BOM, así que se abren bien con doble clic en Excel en español. Cada archivo tiene 22 columnas:

| Columna | Contenido |
|---------|-----------|
| `nombre` | Nombre del negocio |
| `rubro` | Rubro general del catálogo (por ejemplo, "Gastronomía") |
| `rubro_google` | Categoría de Google (por ejemplo, "Pizzería") |
| `telefono` | Teléfono tal como figura en Google (los negocios sin teléfono no se exportan) |
| `whatsapp` | Celular en formato internacional (`5493513254578`) |
| `link_whatsapp` | Link directo al chat (`https://wa.me/...`) |
| `emails` | Emails encontrados en la web del negocio |
| `sitio_web` | Web propia (sin redes sociales) |
| `instagram` | Perfil de Instagram |
| `facebook` | Página de Facebook |
| `direccion` | Dirección |
| `localidad` | Localidad oficial (Georef) |
| `partido` | Partido o departamento oficial (Georef) |
| `provincia` | Provincia (o CABA) |
| `codigo_postal` | Código postal |
| `localidad_google` | Ciudad tal como la escribe Google |
| `latitud`, `longitud` | Coordenadas |
| `rating`, `cantidad_resenas` | Calificación y cantidad de reseñas |
| `link_google_maps` | Ficha en Google Maps |
| `place_id` | Identificador único de Google |

Los estados de cada rubro en `PROGRESO.txt` son:

| Estado | Significado |
|--------|-------------|
| `pending` | Todavía no empezó |
| `running` | Scrapeando ahora |
| `paused` | Llegó a su parte por ahora; puede recibir más en otra vuelta |
| `done` | Terminado: alcanzó su tope o se quedó sin negocios |

## Proxies

Google bloquea por IP cuando ve miles de búsquedas desde la misma conexión. El scraper hace una pausa ante un bloqueo y luego continúa, pero no avanza durante la pausa. Para evitar bloqueos hay que repartir las búsquedas entre muchas IPs:

1. Contratar proxies **residenciales rotativos** (los de datacenter se bloquean rápido).
2. Crear `leads-output/proxies.txt` con un proxy por línea:
   ```
   http://usuario:clave@host:puerto
   socks5://usuario:clave@host2:puerto
   ```
3. Ejecutar `iniciar` o `reanudar`: los proxies se usan automáticamente. `diagnostico` muestra cuántos hay en uso.

Sin proxies, bajar la velocidad reduce los bloqueos: `CONCURRENCY=2 ./leads.sh iniciar cordoba 50000`.

## Opciones

| Variable | Uso | Ejemplo |
|----------|-----|---------|
| `RUBROS` | Elegir rubros y su orden | `RUBROS=salud,abogados ./leads.sh iniciar cordoba 5000` |
| `CONCURRENCY` | Navegadores en paralelo (por defecto 4) | `CONCURRENCY=2 ./leads.sh iniciar cordoba 50000` |

Las opciones se guardan con `iniciar` y `reanudar` las reutiliza.

## Rubros

`./leads.sh rubros` muestra la lista. Siguen las categorías de dir.ar, más belleza y contadores:

gastronomia · kioscos-y-almacenes · supermercados-y-mayoristas · panaderias · carnicerias · alimentos-y-bebidas · salud · farmacias · construccion · ferreterias · inmobiliarias · administraciones-de-consorcios · arquitectos · talleres-mecanicos · concesionarias · repuestos-y-lubricentros · gomerias · ropa · belleza-y-estetica · hogar-y-electrodomesticos · bazar-regalerias-papeleras · celulares-electronica-informatica · educacion · gimnasios · clubes-y-deportes · veterinarias-y-mascotas · hoteles-y-alojamientos · turismo-y-viajes · transporte-y-logistica · seguros · abogados · contadores · marketing-y-publicidad · graficas-e-imprentas · catering-y-eventos · geriatricos · agropecuario · fabricas-e-industrias · comercio-exterior

## Limitaciones conocidas

- **WhatsApp:** solo se detectan los celulares que Google muestra con `15` después de la característica (`0351 15-...`). Los celulares cargados sin el 15 no se distinguen de un fijo y quedan sin WhatsApp.
- **Emails:** solo se obtienen de webs propias. Instagram y Facebook no los exponen.
- **Topes aproximados:** el conteo se hace una vez por minuto, así que un rubro puede pasarse un poco de su objetivo.
- **Repetidos entre rubros:** un mismo negocio puede aparecer en dos rubros (por ejemplo, una cafetería que también es panadería). Dentro de cada archivo no hay repetidos.
- **Negocios cerrados:** los marcados como "cerrado permanentemente" se excluyen.
- **Velocidad:** con emails, del orden de 60 leads por minuto con una sola IP. Los rubros grandes de Buenos Aires pueden llevar días.
