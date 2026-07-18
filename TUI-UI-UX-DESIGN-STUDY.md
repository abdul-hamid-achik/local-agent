# Estudio de UI/UX y arquitectura espacial del TUI

Estado: propuesta de diseño e implementación, no ADR

Fecha de la revisión: 2026-07-17

HEAD final revisado: `0a84364b819a790461c33cbccb039e6fc7a3dd65`

Base del tercer pase del TUI: `6b3246b8e1813d82d6cd68cf2f6a44c307fe39fe`

Visibilidad: documento de ingeniería del repositorio, deliberadamente
publicable; no contiene material interno, paths privados ni artefactos
temporales

## Resumen ejecutivo

Local Agent no necesita imitar visualmente a Crush, Oh My Pi, Zero ni Grok.
Ya tiene tres ventajas que conviene conservar:

1. un composer y una cola de follow-ups especialmente sólidos;
2. permisos inline seguros, con denegación como opción inicial;
3. una representación de tools que no confunde transporte exitoso con éxito de
   dominio ni con evidencia verificada.

La mejora de mayor impacto no es un nuevo color, borde o sidebar. Es construir
una sola autoridad espacial para todo el frame y convertir el transcript en un
documento semántico con identidad estable.

La dirección recomendada es:

- calcular una única `FrameProjection` por frame;
- representar cada elemento del chat como un bloque con `BlockID`, revisión y
  ciclo de vida;
- preservar la lectura mediante un ancla semántica, no mediante un `YOffset`;
- separar ancho de lectura y ancho de trabajo;
- mantener tools compactas en el chat y abrir inspectores especializados para
  detalle completo;
- convertir la consulta de expertos existente en el primer adaptador de una
  superficie general de agentes y subagentes;
- hacer que render, cursor, foco, mouse, selección y scroll consuman la misma
  geometría half-open;
- mantener la capa multi-provider fuera de esta implementación hasta que su
  contrato esté estabilizado.

El resultado deseado no es “verse como otro harness”. Es que Local Agent se
sienta estable, legible y predecible incluso cuando hay streaming, tools,
permisos, varios agentes, un resize y un usuario leyendo lejos del final.

## 1. Alcance, método y frontera clean-room

### 1.1 Qué se estudió

La revisión cubrió:

- composición completa del frame;
- posiciones, paddings, gutters, gaps y breakpoints;
- cálculo de alturas y anchos;
- transcript, streaming, thinking y attachments;
- composer, cola y completion;
- cards de tools, output y diffs;
- permisos, preguntas, planes y formularios;
- agentes, subagentes, tareas y viewers;
- overlays, foco, cursor, mouse y hit testing;
- scroll, búsqueda, selección y restauración tras reflow;
- temas, contraste, `NO_COLOR`, movimiento reducido y glifos;
- caching, virtualización y coste por frame.

Se hizo una lectura estática de los repositorios fijados a revisiones concretas
y una auditoría independiente de Local Agent. No se copiaron componentes,
strings, snapshots ni algoritmos distintivos.

### 1.2 Repositorios y revisiones

| Proyecto | Revisión estudiada | Licencia declarada | Papel en el estudio |
|---|---|---|---|
| [Crush](https://github.com/charmbracelet/crush/tree/037adac527f6dcb7019b56de5051fe3eec0adf08) | `037adac527f6dcb7019b56de5051fe3eec0adf08` | FSL-1.1-MIT Future License | Claridad espacial, rail visual, composer, permisos y agentes inline |
| [Oh My Pi](https://github.com/can1357/oh-my-pi/tree/0f9fceeea483caad531a32b050ac38558516cb5c) | `0f9fceeea483caad531a32b050ac38558516cb5c` | MIT | Frontera entre historial estable y HUD vivo, overlays y Agent Hub |
| [Zero](https://github.com/Gitlawb/zero/tree/e666395d47a059a54c9cf00ab251d127ecc4f21f) | `e666395d47a059a54c9cf00ab251d127ecc4f21f` | MIT | Gramática densa, virtualización, diffs, sidebar y especialistas |
| [Grok Build de xAI](https://github.com/xai-org/grok-build/tree/8adf9013a0929e5c7f1d4e849492d2387837a28d) | `8adf9013a0929e5c7f1d4e849492d2387837a28d` | Apache-2.0 | Documento virtual, medición exacta, search, tools especializadas y subagentes |
| Local Agent | `0a84364b819a790461c33cbccb039e6fc7a3dd65` | la del proyecto | Base TUI en `6b3246b`; delta multi-provider hasta el HEAD final auditado |

Crush requiere especial cuidado: su revisión está bajo FSL-1.1-MIT Future
License. De Crush sólo se toman principios observables, invariantes y
conclusiones geométricas. Esta propuesta es clean-room y usa nombres,
estructuras y contratos propios de Local Agent.

### 1.3 Qué no cubre este documento

- No implementa cambios.
- No es una ADR.
- No cambia el contrato público del sitio en `docs/`.
- No prescribe una estética idéntica a ningún referente.
- No evalúa la calidad de modelos o providers.
- Sí revisa las superficies UI añadidas por el commit multi-provider que
  aterrizó durante la redacción.
- No autoriza a persistir razonamiento privado, prompts internos ni payloads
  MCP sin proyectar.

## 2. Resultado comparativo

### 2.1 Fortalezas y límites de cada referente

| Sistema | Lo mejor | Lo que no conviene heredar |
|---|---|---|
| Crush | Superficies claras; chat legible; composer estable; tools, preguntas y agentes comparten gramática; permisos mantienen contexto | Dos tiers con salto brusco; hit testing horizontal permisivo; algunos controles sólo parecen clicables; restricciones de licencia |
| Oh My Pi | Distingue historial asentado de contenido vivo; mantiene orden causal; overlays con restauración de foco; Hub y viewer de agentes | Renderer ANSI y scrollback nativo demasiado complejos para portarlos; permisos binarios; accesibilidad repartida en varios flags |
| Zero | Alta densidad; prosa limitada; tools sin cajas innecesarias; diffs fuertes; transcript virtualizado; sidebar operacional | Varias fuentes de ancho; sidebar puede reducir el chat al cruzar un breakpoint; footer puede asfixiar el transcript; overlays provocan reflow |
| Grok Build | Layout puro; altura mínima del scrollback; paint window; anclas y búsqueda; tools especializadas; tareas y subagentes con vistas dedicadas | Complejidad alta; no toda su arquitectura es necesaria para la primera iteración; usa Ratatui, no Charm |
| Local Agent | Composer, cola, approvals, seguridad terminal, tema adaptativo y semántica transporte/dominio/evidencia | Dos autoridades del frame; transcript como string; scroll numérico; tools poco especializadas; no hay superficie general de agentes |

### 2.2 Lecciones que sí convergen

Los cuatro referentes coinciden, por caminos distintos, en varios principios:

1. la conversación debe tener ritmo de documento, no una caja por evento;
2. prosa y código no necesitan el mismo ancho;
3. el composer debe crecer con límite y conservar siempre una zona de chat;
4. una tool es una entidad con ciclo de vida, no texto que se reemplaza;
5. lo vivo y lo asentado necesitan tratamiento distinto;
6. detalle operacional debe revelarse progresivamente;
7. un agente hijo necesita identidad, estado y una ruta de inspección;
8. los overlays necesitan ownership de foco y presupuesto espacial;
9. resize y streaming no deben robar la posición de lectura;
10. la geometría de paint y la de interacción deben ser la misma.

## 3. Auditoría del TUI actual de Local Agent

### 3.1 Lo que debe preservarse

#### Mínimo terminal seguro

El mínimo soportado ya es explícito: `30×12`. Cuando el terminal no cabe, la
interacción se pausa y aparece una recuperación clara
(`internal/ui/layout.go:7-13`, `internal/ui/layout.go:72-93`). Esto es mejor que
intentar pintar un frame imposible.

#### Conversación full-width

El chat ocupa deliberadamente el ancho principal y los controles poco
frecuentes viven en overlays (`internal/ui/view.go:29-32`). Esta decisión evita
el problema observado en Zero: a `W=80`, su sidebar deja aproximadamente 51
columnas al chat.

#### Composer y cola

El composer actual:

- usa el `textarea` de Bubbles;
- permite redactar mientras el turno sigue activo;
- muestra overflow en lugar de ocultarlo;
- conserva una sola continuación en cola;
- permite recuperar, editar o eliminar esa cola;
- mantiene el cursor hardware bajo el control del padre;
- limita la altura visible a ocho filas.

Las ramas principales están en `internal/ui/view.go:70-139`,
`internal/ui/composer_flow.go` e `internal/ui/model.go:1134-1143`. No se
recomienda reemplazar este flujo.

#### Permisos

Los permisos reemplazan el composer en el flujo, conservan el transcript
visible, usan un viewport interno y comienzan en una elección segura
(`internal/ui/view.go:42-49`, `internal/ui/approval.go`). Esta es una ventaja
de producto y de seguridad.

#### Semántica de tools

`ToolCard` distingue:

- transporte;
- resultado de dominio;
- evidencia;
- estado desconocido o que requiere atención.

La proyección sólo dibuja éxito cuando corresponde
(`internal/ui/toolcard.go:459-479`). Debe mantenerse la frontera de
`internal/ecosystem`: un MCP que respondió no equivale a una operación de
dominio exitosa ni a evidencia verificada.

#### Seguridad y accesibilidad base

La implementación ya tiene:

- `lipgloss.LightDark()`;
- `NO_COLOR`;
- movimiento reducido;
- un solo cursor terminal;
- saneamiento de output;
- límites para tool results;
- caches para prefijo estable y entradas asentadas.

#### Integración multi-provider ya aterrizada

Mientras se redactaba este estudio, `main` avanzó de `6b3246b` a `0a84364b`.
El delta sí añadió superficies de UI y fue revisado antes de cerrar el
documento:

- `ProviderPickerState` usa `list.Model` de Bubbles y reutiliza el frame de los
  pickers (`internal/ui/providerpicker.go:12-133`), pero todavía no participa
  en `resizePickerOverlays` ni `restylePickerOverlays`
  (`internal/ui/settingspicker.go:145-215`,
  `internal/ui/picker_style.go:115-158`), ni propaga reduced motion al cursor
  de filtro (`internal/ui/providerpicker.go:52-76`,
  `internal/ui/picker_style.go:102`);
- el label normal pone la frontera remota antes del nombre del modelo
  (`internal/ui/model_location.go:5-53`), pero el fallback más estrecho
  conserva sólo la primera palabra y puede ocultar `remote prompts`
  (`internal/ui/view_footer.go:180-200`);
- la preferencia manual de provider se persiste en una frontera distinta de la
  sesión (`internal/ui/model_preference.go:5-17`,
  `internal/ui/model_preference.go:50-63`);
- `OverlayProviderPicker` se añadió al enum plano y a las ramas de
  `View`/`Update` (`internal/ui/model_types.go:22-44`,
  `internal/ui/view.go:142-193`, `internal/ui/model.go:828-833`);
- el provider picker sí posee el teclado y suprime el cursor subyacente, pero
  sólo puede volver a un padre `Settings`; es una jerarquía de un nivel, no un
  modal stack (`internal/ui/keys_overlay.go:141-152`,
  `internal/ui/overlay_nav.go:3-30`);
- wheel y click quedan consumidos por el overlay, pero no se envían a su lista
  (`internal/ui/update_terminal.go:207-232`,
  `internal/ui/update_terminal.go:247-260`);
- seleccionar un provider todavía agrega un evento textual y vuelve a llamar
  `SetContent(renderEntries())`; tanto éxito como error terminan forzando
  `FollowLatest` (`internal/ui/providerpicker.go:102-115`);
- confirmar el provider llama `SwitchProvider` sincrónicamente dentro de
  `Update`; ese camino espera locks y el retorno a Ollama puede intentar un
  refresh local de hasta dos segundos
  (`internal/ui/provider.go:44-48`, `internal/llm/manager.go:181-224`).

El cambio no invalida la dirección de `FrameProjection`, `BlockID` ni anchor
semántico. La refuerza: hay una superficie más que debe compartir geometría y
foco, y un nuevo evento de preferencia/transcript que debe entrar por el
reconciler sin confundirse con estado de sesión. También revela un requisito:
la localidad explícita debe sobrevivir al tier más estrecho, no quedar
reducida únicamente al nombre del provider.

### 3.2 Problema P0: dos autoridades del frame

`View()` compone transcript, divisor, status, planes, contexto, cola, composer,
formularios y overlays (`internal/ui/view.go:12-225`).

Por separado, `footerHeight()` vuelve a recorrer casi las mismas ramas, llama
otra vez a renderers y mide sus strings (`internal/ui/model.go:1055-1122`).

Esto produce:

- riesgo de que medida y paint diverjan;
- trabajo duplicado;
- cálculos de cursor basados en contar `\n`;
- dificultad para definir hit rects exactos;
- tests que prueban resultados, pero no una única ley espacial;
- más coste cada vez que aparece una nueva superficie.

La solución no es añadir más condiciones a `footerHeight()`. Es eliminar la
doble autoridad.

### 3.3 Problema P0: transcript sin identidad espacial

El transcript tiene caches útiles, pero se materializa como un string completo
y el memo se indexa por posición (`internal/ui/view_transcript.go:47-157`,
`internal/ui/view_transcript.go:199-239`).

Consecuencias:

- insertar o retirar una entrada cambia identidades posicionales;
- `viewport.SetContent()` vuelve a recibir el documento completo en muchos
  handlers;
- una expansión antes de la ventana cambia el significado del `YOffset`;
- resize no puede restaurar una línea lógica concreta;
- búsqueda, selección y acciones por bloque no tienen una identidad común;
- virtualizar requiere reconstruir primero el modelo.

### 3.4 Problema P0: scroll numérico

Local Agent distingue correctamente entre seguir el final y lectura pausada.
Sin embargo, la lectura pausada conserva un `YOffset`
(`internal/ui/scroll_follow.go:11-46`).

Si cambia el wrapping, se expande una tool o crece un bloque anterior, el mismo
número apunta a otro contenido. La intención del usuario se conserva; su
posición semántica no.

### 3.5 Gaps P1

- La prosa se vuelve demasiado ancha en terminales grandes.
- Las tools comparten demasiada lógica genérica.
- El diff inline es un preview truncado, no un inspector.
- El equipo de expertos tiene resumen inline, pero no Hub ni viewer.
- `OverlayKind` es plano; no existe una pila general con restauración de foco.
- Los hit targets guardan fila y extremo derecho, pero no un rectángulo
  completo.
- La restauración de checkpoints reconstruye mensajes y limpia tools
  (`internal/ui/update_command.go:477-501`). La sesión nativa sí persiste
  receipts; la brecha es específicamente el restore de checkpoint.
- Existe un helper `truncate()` basado en bytes
  (`internal/ui/view.go:277-282`) que debe eliminarse en favor de truncado por
  celdas.

### 3.6 Terminología a corregir

El “Agent picker” actual selecciona un perfil/configuración; no representa
agentes vivos. Para evitar confusión:

- `Agent Profile` o `Profile` debe nombrar configuración;
- `Agents` debe reservarse para ejecuciones vivas o históricas;
- `Agent Hub` debe ser la superficie operacional;
- `Agent Viewer` debe mostrar el transcript o actividad de un hijo.

## 4. Principios de diseño objetivo

### 4.1 Estabilidad antes que densidad

Una superficie no debe aparecer automáticamente si al hacerlo hace más
estrecho el contenido que el usuario estaba leyendo. Ganar una columna del
terminal nunca debería causar una pérdida repentina de decenas de columnas del
chat.

### 4.2 Jerarquía por ritmo, no por cajas

La conversación debe usar:

- alineación;
- rails;
- gaps;
- peso tipográfico;
- color semántico;
- disclosure.

Las cajas fuertes se reservan para decisiones, errores importantes,
inspectores y overlays.

### 4.3 Resumen inline, detalle navegable

Chat, tools y agentes deben responder rápidamente:

- qué ocurrió;
- en qué estado está;
- qué requiere atención;
- dónde está el detalle.

El transcript no debe convertirse en el lugar donde se muestran miles de
líneas de output.

### 4.4 Capacidad derivada del rectángulo final

Un tier global describe densidad de chrome. No garantiza que un hijo tenga el
ancho necesario. Un diff muestra gutters, por ejemplo, sólo si su rectángulo
final conserva código legible después de descontarlos.

### 4.5 Una acción, un owner de input

En cada frame debe existir un único dueño de teclado y como máximo un cursor.
La precedencia no debe depender de una colección de `if` dispersos.

### 4.6 Sin color como única evidencia

Cada estado usa al menos dos señales entre:

- glifo;
- label;
- posición;
- rail;
- color;
- copy de estado.

### 4.7 Privacidad por proyección

La UI recibe sólo proyecciones bounded:

- no muestra razonamiento privado;
- no conserva prompts internos de subagentes;
- no concatena `StructuredContent` MCP en el transcript;
- no persiste output de display con ANSI;
- no transforma transporte exitoso en evidencia.

## 5. Contrato geométrico

### 5.1 Coordenadas

Todos los rectángulos usan máximos exclusivos:

```text
Rect = [MinX, MaxX) × [MinY, MaxY)

Width  = max(0, MaxX - MinX)
Height = max(0, MaxY - MinY)

Contains(x, y) =
  MinX <= x && x < MaxX &&
  MinY <= y && y < MaxY
```

El espacio físico es:

```text
Screen = [0, W) × [0, H)
```

`W=0` o `H=0` produce un frame vacío, cursor `nil` y cero hit regions. No se
debe inventar una celda con `max(1, width)`.

### 5.2 Insets terminales explícitos

Hoy varios caminos reservan implícitamente una columna derecha y una fila
final. El rediseño debe expresarlo:

```go
type TerminalInsets struct {
    Top, Right, Bottom, Left int
}
```

`SafeScreen = Inset(Screen, TerminalInsets)`.

El valor objetivo debe decidirse con tests PTY. Durante migración se puede
usar el comportamiento actual como perfil de compatibilidad, pero ningún hijo
debe conocer ni repetir ese `-1`.

### 5.3 Primitivas de split

Toda resta se limita antes de ejecutarse:

```text
TakeBottom(rect, n):
  n      = clamp(n, 0, rect.Height)
  taken  = [MinX, MaxX) × [MaxY-n, MaxY)
  remain = [MinX, MaxX) × [MinY, MaxY-n)
```

Después de cada split:

```text
taken ⊆ parent
remain ⊆ parent
taken ∩ remain = ∅
area(taken) + area(remain) = area(parent)
```

La misma ley aplica a `TakeTop`, `TakeLeft` y `TakeRight`.

### 5.4 Tiers en dos ejes

Ancho:

```text
W < 30        Recovery
[30, 40)      Compact
[40, 72)      Narrow
[72, 112)     Regular
[112, ∞)      Wide
```

Alto:

```text
H < 12        Recovery
[12, 16)      Compact
[16, 24)      Short
[24, 40)      Regular
[40, ∞)       Tall
```

La aplicación entra en Recovery si cualquiera de los ejes está en Recovery.
No se combinan los dos enums. `112×16` es ancho y corto; `40×40` es estrecho y
alto.

### 5.5 Capabilities

Los tiers alimentan una evaluación posterior:

```go
type LayoutCapabilities struct {
    CanDockContext      bool
    CanShowRichHeader   bool
    CanShowDiffGutters  bool
    CanShowDualGutters  bool
    CanUseSplitDiff     bool
    CanStackAuxiliary   bool
    CanShowAgentPreview bool
}
```

Cada capability se calcula sobre el rectángulo final del componente.

Ejemplo:

```text
CanShowDualGutters =
  codeWidthAfterDualGutters >= 40
```

No:

```text
CanShowDualGutters = terminalWidth >= 72
```

### 5.6 Tokens espaciales

Perfil inicial:

| Token | Compact | Narrow | Regular/Wide |
|---|---:|---:|---:|
| padding exterior por lado | 1 | 1 | 1 |
| rail | 1 | 1 | 1 |
| gap rail-contenido | 1 | 1 | 1 |
| gap entre bloques del mismo grupo | 0 | 0 | 0 |
| gap entre turnos | 1 fila | 1 fila | 1 fila |
| gap panel-chat | 1 | 1 | 1 |
| padding de modal | 1 | 1 | 1 |
| ancho máximo de prosa | ancho útil | ancho útil | 96 |

Son tokens de diseño, no offsets embebidos en renderers.

### 5.7 Ancho de lectura y ancho de trabajo

```text
ProseTargetCandidate = 96
WorkWidth             = ChatContentRect.Width
ProseWidth            = min(ProseTargetCandidate, WorkWidth)
```

`96` es el candidato usado para hacer verificables las fórmulas y wireframes
de este estudio, no una decisión final. El prototipo debe compararlo con 104 y
112 sin cambiar el contrato `ProseWidth <= WorkWidth`.

Usan `ProseWidth`:

- párrafos;
- listas;
- blockquotes;
- mensajes del sistema;
- explicaciones de error.

Usan `WorkWidth`:

- code fences;
- tablas;
- diffs;
- logs;
- resultados estructurados;
- formularios;
- inspectores.

El eje izquierdo permanece compartido. El sobrante de prosa queda a la
derecha; no se centra cada párrafo porque eso movería el inicio visual entre
tipos de bloque.

### 5.8 Panel contextual

El panel amplio es una capacidad opcional, no un efecto automático del
breakpoint.

Variables:

```text
A      ancho útil de TranscriptWork después de TerminalInsets y padding exterior
K      gutters permanentes del chat
G      gap entre chat y panel
Cmin   mínimo útil del chat
Pmin   mínimo útil del panel
Ppref  ancho preferido del panel
Pmax   máximo del panel
```

`PanelPreference ∈ {Hidden, Drawer, Docked}`.

Perfil inicial:

```text
K=2
G=1
Cmin=72
Pmin=28
Ppref=32
Pmax=36
```

Fórmula:

```text
Plimit = A - G - K - Cmin

CanDock =
  WidthClass == Wide &&
  HeightClass in {Regular, Tall} &&
  Plimit >= Pmin

ShouldDock =
  CanDock &&
  PanelPreference == Docked &&
  !RedockRequiresConfirmation

PanelInteractive = ShouldDock && noBlockingOverlay

EffectivePanelMode =
  Hidden  if PanelPreference == Hidden
  Docked  if ShouldDock
  Drawer  otherwise

if ShouldDock:
  P      = clamp(Ppref, Pmin, min(Pmax, Plimit))
  Couter = A - G - P
else:
  P      = 0
  Couter = A

Cuseful = max(0, Couter - K)
```

Reglas:

- `Wide` habilita docking, pero no lo activa.
- `CanDock` expresa capacidad; `PanelPreference` expresa intención persistida;
  `EffectivePanelMode` expresa el estado pintado.
- Una acción `Dock` fija `PanelPreference=Docked` y limpia
  `RedockRequiresConfirmation`.
- Si un resize invalida `CanDock`, `EffectivePanelMode` degrada a `Drawer` y
  arma `RedockRequiresConfirmation`.
- Al hidratar un workspace con preferencia `Docked` pero sin capacidad
  geométrica, se arma el mismo latch antes del primer frame.
- Un resize posterior nunca promueve `Drawer` a `Docked`; requiere una nueva
  acción `Dock`. Así se conserva la monotonía pasiva.
- Un overlay bloqueante cambia Z, foco e interacción, no `ShouldDock` ni los
  rectángulos base; el panel queda reservado bajo el scrim.
- `PanelPreference`, `EffectivePanelMode` y el latch de confirmación pueden
  persistirse por workspace.
- Si no caben 72 columnas útiles, el panel se vuelve drawer/overlay.
- Para dockear sin degradar el candidato de prosa se requieren 96 columnas
  útiles. Con el perfil anterior y panel 32, esto ocurre a partir de `A=131`;
  `A` no es el ancho del terminal, porque ya descontó insets y padding.
- El panel no se oculta por terminar una animación o porque una lista quedó
  vacía; eso produciría reflow inesperado.

### 5.9 Asignación vertical

El transcript reserva su suelo antes de dejar crecer superficies inferiores:

```text
TranscriptMin(H) = clamp(floor(H/3), 4, 12)
DraftOwnerMax(H) = clamp(floor(H/3), 4, 12)
StatusFloor      = 1
DraftOwnerFloor  = 3
```

Algoritmo base:

```text
r = SafeScreen

Status, r = TakeBottom(r, 1)
Header, r = TakeTop(r, HeaderDesired(HeightClass))

owner    = ResolveActiveOwner(state)
ownerMin = clamp(owner.MinHeight(ctx), 0, r.Height)

Tmin = TranscriptMin(H)
if r.Height < Tmin + ownerMin:
  Recovery

ownerBudget = r.Height - Tmin
ownerMax    = clamp(
  owner.MaxHeight(ctx.WithAvailable(ownerBudget)),
  ownerMin,
  ownerBudget,
)
OwnerHeight = clamp(
  owner.DesiredHeight(ctx),
  ownerMin,
  ownerMax,
)
ActiveOwner, r = TakeBottom(r, OwnerHeight)

for auxiliary in priorityOrder:
  h = min(auxiliary.DesiredHeight, max(0, r.Height - Tmin))
  Auxiliary[i], r = TakeBottom(r, h)

Transcript = r
```

El owner activo hace scroll interno cuando excede su cap. El draft usa
`DraftOwnerFloor/DraftOwnerMax`; permisos y formularios exponen sus propios
límites. Ningún owner toma filas del suelo del transcript.

### 5.10 Orden de degradación vertical

1. eliminar gaps decorativos;
2. reducir header rico a header compacto;
3. condensar metadata de status;
4. colapsar auxiliares opcionales;
5. mover plan/agents/context a drawer;
6. hacer scroll interno en pregunta o permiso;
7. limitar composer y mostrar overflow;
8. conservar transcript mínimo;
9. entrar en Recovery si no cabe la autoridad de input segura.

Completion y menús flotantes se superponen; no consumen permanentemente filas
del transcript.

### 5.11 Monotonía

Para estado de UI constante:

```text
TranscriptHeight(H+1) >= TranscriptHeight(H)
```

También:

```text
ChatUsefulWidth(W+1) >= ChatUsefulWidth(W)
```

La segunda propiedad no aplica a una acción explícita del usuario que abra un
panel; sí aplica a un resize pasivo.

## 6. `FrameProjection`: una sola autoridad

### 6.1 Modelo

```go
type FrameProjection struct {
    Screen       CellRect
    SafeScreen   CellRect
    Header       SurfaceProjection
    Transcript   TranscriptProjection
    Auxiliaries  []SurfaceProjection
    Composer     SurfaceProjection
    Status       SurfaceProjection
    ContextPanel *SurfaceProjection
    Overlays     []OverlayProjection
    Focus        FocusToken
    Cursor       *tea.Cursor
    HitRegions   []HitRegion
}

type SurfaceProjection struct {
    ID       SurfaceID
    Rect     CellRect
    Content  string
    Visible  bool
    Z        int
}
```

`FrameProjection` se calcula una vez tras `Update`, con estado ya reconciliado.
`View()` sólo compone sus superficies.

### 6.2 Lo que elimina

- `footerHeight()` como segundo renderer;
- contar saltos dentro de strings para ubicar cursor;
- fórmulas de ancho diferentes entre startup, viewport y input;
- hit regions reconstruidas parcialmente desde texto final;
- overlays que restauran foco “por convención”;
- discrepancias entre resize, paint y mouse.

### 6.3 Smart parent, dumb child

Se conserva el patrón Charm:

- `Model.Update` enruta todos los mensajes;
- hijos reciben rect y estado proyectado;
- hijos exponen intents o `tea.Cmd`;
- el padre decide foco, autoridad, side effects y navegación;
- ningún hijo escribe estado compartido durante `View`.

### 6.4 Pipeline por frame

```text
tea.Msg
  ↓
Model.Update
  ↓
reconciliar eventos semánticos
  ↓
actualizar BlockStore / owners / overlays
  ↓
calcular LayoutSnapshot
  ↓
medir sólo bloques dirty
  ↓
resolver scroll anchor
  ↓
producir FrameProjection
  ↓
View compone strings + cursor
```

## 7. Modelo semántico del transcript

### 7.1 Identidad

```go
type TranscriptBlock struct {
    ID        BlockID
    TurnID    TurnID
    ParentID  BlockID
    Kind      BlockKind
    Revision  uint64
    Lifecycle BlockLifecycle
    Payload   BlockPayload
}

type BlockLifecycle uint8

const (
    BlockPending BlockLifecycle = iota
    BlockLive
    BlockSettling
    BlockSettled
    BlockFailed
    BlockCancelled
)
```

`BlockID` es estable durante persistencia, restore y reflow. `Revision` cambia
sólo cuando cambia el payload semántico o el lifecycle; width, theme, density,
foco y expansión no la incrementan. Una tool conserva el mismo ID desde
pending hasta settled.

### 7.2 Tipos de bloque

- `UserMessage`
- `AssistantMessage`
- `ReasoningSummary`
- `ToolGroup`
- `ToolCall`
- `AgentGroup`
- `AgentEvent`
- `PermissionReceipt`
- `QuestionReceipt`
- `PlanEvent`
- `SystemNotice`
- `ErrorNotice`
- `CompactionEvent`
- `SessionBoundary`

### 7.3 Orden causal

El orden del transcript debe reflejar el orden observable:

```text
user
assistant text prefix
tool call
tool result
assistant continuation
agent group
assistant final
```

No se agrupan todas las tools al final del turno si originalmente ocurrieron
entre dos segmentos del assistant.

### 7.4 Matriz de separación

| Anterior | Siguiente | Separación |
|---|---|---:|
| user | assistant/reasoning/tool | 1 fila |
| assistant | tool | 0 filas dentro del mismo turno |
| tool | tool del mismo grupo | 0 filas |
| tool | assistant continuación | 0 filas |
| reasoning summary | assistant | 0 filas |
| cualquier cierre de turno | siguiente user | 1 fila |
| system | system contiguo | 0 filas |
| error crítico | cualquier bloque | 1 fila |

La separación pertenece al reconciler de transcript, no a cada renderer.

### 7.5 Gramática visual

Usuario:

```text
you
▌ texto del usuario
  segunda línea
```

Assistant:

```text
assistant
  prosa hasta `ProseTargetCandidate` (96 en el perfil inicial)
```

Tool:

```text
│ ▸ ✓ Read · internal/ui/layout.go · 42 lines
```

Reasoning summary:

```text
│ ▸ Thinking · 12 s · summary available
```

El header `assistant` aparece una vez por tramo causal, no antes de cada tool.

### 7.6 Prosa y contenido mixto

Un mensaje Markdown se segmenta:

```go
type MarkdownSection struct {
    Kind     SectionKind
    SourceID SectionID
    Text     string
}
```

Párrafo/lista/quote usa `ProseWidth`; fence/tabla usa `WorkWidth`. El cache se
indexa por:

```text
BlockID + Revision + SectionID + Width + ThemeKey + Density + ExpansionKey
```

### 7.7 Streaming

Estados:

```text
pending → streaming → settling → settled
                    ↘ failed
                    ↘ cancelled
```

Reglas:

- el prefijo Markdown asentado permanece cacheado;
- sólo la sección live se vuelve a renderizar;
- el cursor de lectura no salta cuando crece contenido debajo;
- si follow está activo, el final sigue visible;
- al finalizar se hace un render Markdown completo una sola vez;
- el bloque conserva ID y posición causal.

### 7.8 Thinking

La UI no debe mostrar razonamiento privado. Sólo puede representar una
proyección host-safe como:

- estado (`thinking`, `compacting`, `verifying`);
- tiempo;
- resumen explícitamente permitido;
- últimas líneas de una salida pública de trabajo, si el contrato del provider
  lo autoriza.

Comportamiento:

- live: una línea o máximo tres filas;
- settled: colapsado por default;
- expanded: viewer bounded;
- reduced motion: glifo estático;
- sin resumen autorizado: mostrar sólo lifecycle, no contenido.

### 7.9 Attachments

Los attachments forman una fila de chips dentro del presupuesto del composer o
del mensaje:

```text
[image.png 1.2 MB] [design.md] [+2]
```

Cada chip tiene acción propia y label accesible. En narrow se envuelven; en
compact se resumen como `3 attachments`.

### 7.10 Error y recuperación

Un error tiene:

- bloque durable en el punto causal;
- resumen visible aunque esté colapsado;
- acción `jump to error` si follow está pausado;
- acción de retry sólo cuando la operación es idempotente o el host puede
  explicar el riesgo;
- detalle técnico en viewer, no como pared de texto;
- copy distinto para transporte, dominio, validación y evidencia.

## 8. Scroll, anclas y virtualización

### 8.1 Intención de scroll

```go
type ScrollIntent interface {
    isScrollIntent()
}

type FollowLatest struct{}

type ManualAnchor struct {
    SessionID     SessionID
    BlockID       BlockID
    LogicalOffset int
    Grapheme      int
    ScreenRow     int
    Bias          AnchorBias
}
```

`BlockID` solo no basta: un bloque puede medir cientos de filas.

### 8.2 Restauración

```text
mappedRow = fila global del punto semántico
maxTop    = max(0, totalHeight - viewportHeight)
globalTop = clamp(mappedRow - ScreenRow, 0, maxTop)
```

Fallback:

1. mismo `BlockID` y offset;
2. siguiente bloque sobreviviente si `Bias=next`;
3. bloque anterior;
4. inicio del turno;
5. top del documento.

### 8.3 Reglas durante cambios

- crecimiento debajo del viewport no lo mueve;
- crecimiento encima compensa por delta;
- resize resuelve el mismo offset lógico en el nuevo `LineMap`;
- expand/collapse conserva el punto semántico;
- cambiar de sesión restaura el anchor propio de esa sesión;
- abrir un overlay no cambia el anchor;
- abrir o cerrar el provider picker no cambia el anchor;
- confirmar un provider agrega su receipt sin forzar `FollowLatest` cuando el
  usuario estaba en `ManualAnchor`; si ya seguía el final, continúa siguiéndolo;
- volver a Follow borra el anchor manual y muestra el final.

### 8.4 Índice de layout

```go
type LayoutRecord struct {
    BlockID  BlockID
    Revision uint64
    Height   int
    StartRow int
    Exact    bool
    LineMap  LineMap
}
```

Un árbol Fenwick, segment tree o prefix sums reconstruibles puede resolver:

- fila → bloque;
- bloque → fila;
- delta de alturas;
- rango visible.

La primera iteración puede usar prefix sums con rebuild bounded. La interfaz no
debe acoplarse a una implementación concreta.

### 8.5 Paint window

```text
visibleStart = first block intersecting viewportTop
visibleEnd   = first block after viewportBottom
paintRange   = [visibleStart, visibleEnd + smallMargin)
```

Sólo `paintRange` se renderiza con exactitud. Las alturas asentadas se cachean.
Spinner ticks no recorren el historial completo.

### 8.6 Búsqueda

El índice contiene texto visible saneado:

```go
type SearchEntry struct {
    BlockID    BlockID
    Revision   uint64
    PlainText  string
    MatchSpans []LogicalSpan
}
```

Acciones:

- `/` abre búsqueda;
- `Enter`/`Shift+Enter` navega resultados;
- `Esc` cierra sin mover el anchor original;
- el match activo se mapea a `BlockID + LogicalOffset`;
- el highlight es post-paint y no altera wrapping.

## 9. Tools

### 9.1 Contrato de datos

```go
type ToolViewModel struct {
    InvocationID InvocationID
    BlockID      BlockID
    Kind         ToolKind
    Operation    string
    Target       string
    Lifecycle    ToolLifecycle
    Transport    TransportState
    Domain       DomainState
    Evidence     EvidenceState
    Summary      string
    Preview      ToolPreview
    ArtifactRef  *ArtifactRef
    Duration     time.Duration
    Revision     uint64
}
```

`StructuredContent` no entra en este modelo. El parser lo convierte a una
proyección bounded dentro de la frontera del agente.

### 9.2 Matriz de estado

| Transporte | Dominio | Evidencia | Estado visual |
|---|---|---|---|
| pending/running | unknown | unknown | running |
| failed | cualquiera | cualquiera | error |
| success | failed | cualquiera | error |
| success | unknown | cualquiera | attention |
| success | success | stale/contradicted | attention |
| success | success | unavailable | success de dominio + badge sin evidencia |
| success | success | verified | success + evidencia verificada |

El badge de evidencia es separado. Nunca colorea por sí solo toda la card como
éxito.

### 9.3 Anatomía de header

```text
rail disclosure state operation · target/summary          duration
```

Presupuesto:

```text
Inner = CardWidth - RailWidth - RailGap

Fixed = Disclosure + StateGlyph
Available = Inner - Fixed - mandatoryGaps
```

Orden de degradación:

1. ocultar duration;
2. reducir summary;
3. ocultar summary;
4. truncar target por celdas;
5. conservar operación y estado;
6. en ancho extremo, usar una segunda fila.

La summary nunca ocupa más de 45–50% del espacio flexible mientras el nombre
necesite ancho.

### 9.4 Estados de disclosure

- running: expandible si existe preview útil;
- success: colapsado por default;
- attention: una línea explicativa siempre visible;
- error: mensaje compacto siempre visible;
- cancelled: label explícito, no error genérico;
- expanded: `▾`;
- collapsed: `▸`;
- no detalle: espacio reservado sólo si evita jitter, sin chevron falso.

### 9.5 Renderers especializados

Todos usan chrome común, pero contenido distinto:

| Kind | Collapsed | Preview expanded | Viewer |
|---|---|---|---|
| Read | archivo + rango/line count | primeras 5 + últimas 3 con números absolutos | archivo/rango completo bounded |
| Search | patrón + scope + hits | agrupado por archivo, máximo por filas | resultados completos con navegación |
| Exec | comando/label + exit | primeras 2 + últimas 3; live tail máximo 12–16 | output paginado, copy y search |
| Edit/Write | archivo + `+N/-N` | diff unified | Diff Viewer |
| Git | operación + repo/ref | resumen semántico | detalles y diff/log |
| Agent | tipo + estado agregado | filas activas/atención | Agent Hub/Viewer |
| Generic | operación + target | resumen bounded | artifact/result viewer |

Los límites se expresan en filas visibles, no sólo bytes.

### 9.6 Output largo

Una preview debe decir cuánto oculta:

```text
… 238 lines hidden · open output
```

El contador viene del resultado original proyectado, no del string ya cortado.
El viewer obtiene páginas bounded o un artifact por referencia. Nunca persiste
ANSI crudo.

### 9.7 Diff

#### Gutters

```text
oldDigits = digits(maxOldLine)
newDigits = digits(maxNewLine)

singleGutter = indent + newDigits + contentGap
dualGutter   = indent + oldDigits + gutterGap + newDigits + contentGap
```

Los gutters aparecen si el código restante conserva un mínimo legible.

#### Wrap

Una línea larga:

```text
  42  43 │ + contenido inicial...
          │ ↳ continuación...
          │ ↳ continuación...
```

Las continuaciones dejan el gutter numérico en blanco y conservan el fondo
semántico. No se hace truncado silencioso por bytes.

#### Unified y split

- Unified es el default.
- Split sólo se habilita cuando cada pane conserva al menos 52 columnas de
  código después de gutters y gap.
- Intralínea tiene prioridad sobre split forzado.
- El usuario puede alternar unified/split sin perder archivo/hunk activo.

#### Diff Viewer

Debe incluir:

- header persistente;
- selector de archivo;
- navegación por hunk;
- unified/split cuando cabe;
- búsqueda;
- copy de línea/hunk/path;
- viewport de Bubbles;
- indicador de contenido truncado en origen;
- retorno al mismo `BlockID`.

### 9.8 Acciones de tool

Cada acción vive en un registry:

```go
type UIAction struct {
    ID       ActionID
    Label    string
    Shortcut key.Binding
    Enabled  bool
    Reason   string
    Target   EntityRef
}
```

Acciones posibles:

- expand/collapse;
- open output;
- open diff;
- copy summary;
- copy path;
- jump to source;
- retry, sólo si es seguro;
- cancel, sólo si sigue en curso.

Teclado, mouse y help consumen el mismo registry.

## 10. Agentes y subagentes

### 10.1 Proyección segura

```go
type WorkNode struct {
    ID         WorkNodeID
    ParentID   *WorkNodeID
    Kind       WorkNodeKind
    Label      string
    Status     WorkNodeStatus
    Activity   string
    ModelLabel string
    Tokens     TokenSummary
    Elapsed    time.Duration
    Unread     int
    Revision   uint64
    ReportRef  *ArtifactRef
}
```

Estados:

```text
queued
running
waiting
completed
failed
cancelled
```

No contiene:

- prompt interno;
- razonamiento;
- transcript crudo no autorizado;
- credenciales;
- paths privados sin redacción;
- payloads completos del provider.

### 10.2 Adaptador inicial

`consult_experts` ya produce una proyección bounded y debe ser el primer
adaptador de `WorkNode`, no una excepción permanente dentro de `ToolCard`.

Mapeo:

```text
consultation invocation → AgentGroupBlock
expert progress item    → WorkNode
final report reference  → ArtifactRef
```

### 10.3 Agent Group inline

La conversación muestra:

```text
│ ▾ ◉ Experts · 3 running · 1 done
│   ◉ TUI audit       reading layout.go          18 s
│   ! Diff review     waiting for evidence       11 s
│   ✓ Provider scan   completed                  32 s
│   +5 more · open Agents
```

Reglas:

- live se autoexpande hasta un máximo de 6–8 filas;
- settled se colapsa;
- running y attention aparecen primero;
- orden estable por grupo, estado, inicio e ID;
- cambios no reordenan continuamente items equivalentes;
- `+N` es una acción real;
- el bloque conserva su posición causal.

### 10.4 Agent Hub

En Compact/Narrow/Regular:

- overlay o superficie fullscreen;
- lista arriba/izquierda;
- inspector abajo/derecha según el rectángulo;
- filtro;
- grupos `Subagents`, `Tasks`, `Watchers` si aplican;
- acciones visibles con razones disabled.

En Wide:

- puede abrirse como drawer;
- sólo se dockea por elección;
- no reemplaza el transcript;
- conserva al menos 72 columnas útiles de chat.

### 10.5 Fila de agente

Una fila responde:

```text
status · label · actividad · elapsed · unread
```

En narrow:

```text
◉ TUI audit
  reading layout.go · 18 s
```

El label recibe prioridad sobre modelo y tokens. Información secundaria pasa al
inspector.

### 10.6 Agent Viewer

El viewer muestra:

- breadcrumb `Parent / Child`;
- tipo, status, modelo, contexto bounded, elapsed;
- transcript o eventos públicos del hijo;
- follow propio;
- búsqueda propia;
- acciones permitidas;
- retorno al anchor del padre.

Es read-only por default. “Steer” sólo aparece si el runtime expone esa
capacidad y debe ser una autoridad de composer explícita. Navegar a un hijo no
envía prompts ni modifica el chat principal.

### 10.7 Cancelación

Antes de cancelar:

- mostrar exactamente qué nodo se afecta;
- distinguir cancel de task y cancel de agent;
- avisar si hay hijos;
- no ofrecer “kill” si el provider/runtime no puede garantizarlo;
- mantener receipt durable con resultado de cancelación.

### 10.8 Empty y error

Agent Hub vacío:

```text
No active agents
Agents created during a run will appear here.
```

Error parcial:

```text
2 agents completed · 1 failed
Open failed agent
```

No se borra el grupo completo porque un hijo terminó.

## 11. Composer y ownership

### 11.1 Unión de owner

```go
type ComposerOwner interface {
    OwnerID() OwnerID
    DesiredHeight(LayoutContext) int
    MinHeight(LayoutContext) int
    MaxHeight(LayoutContext) int
    View(OwnerContext) OwnerProjection
    Update(tea.Msg) (OwnerIntent, tea.Cmd)
}
```

Owners:

- `DraftComposer`
- `PermissionPrompt`
- `ReadScopePrompt`
- `QuestionForm`
- `PlanForm`
- `GoalForm`
- `ProviderSetupPrompt`
- `AgentSteerComposer`

Sólo uno es activo.

### 11.2 Prioridad

```text
terminal recovery
  > permission/read scope
  > destructive confirmation
  > provider/session handoff
  > explicit form owner
  > overlay owner
  > draft composer
```

La prioridad se expresa en una función, no por el orden accidental de varias
ramas.

### 11.3 Altura

```text
DraftRowsMax =
  4                                      si HeightClass == Compact
  min(8, max(5, floor(H/3)))             en otro caso

Desired =
  min(visualRows, DraftRowsMax) + chrome + attachments

DraftSurfaceMax =
  min(DraftOwnerMax(H), availableAfterTranscriptFloor)
```

El cap de ocho filas del textarea se conserva en terminales con espacio. En el
tier de alto mínimo baja a cuatro para proteger el transcript. El owner
completo sigue limitado por el presupuesto del frame; la misma medición
alimenta layout y paint. `PermissionPrompt.MaxHeight` devuelve
`PermissionMax`; Question, Plan, Goal y Provider Setup declaran también su
propio máximo. El padre sólo ejecuta el algoritmo común de `ActiveOwner`.

### 11.4 Cola

Se conserva:

- una continuación visible;
- swap mediante una acción explícita;
- draft y cola como owners separados;
- attachments nunca concatenados silenciosamente;
- restauración tras fallo;
- estado `held` visible.

Mejoras:

- hit rect de la fila completa;
- acciones `edit`, `send now`, `remove`;
- posición fija dentro del bottom stack;
- persistencia provider-neutral.

### 11.5 Completion

Completion flota sobre el transcript/composer, pero:

- nunca oculta el cursor;
- reserva una altura máxima;
- usa lista y preview adaptativos;
- conserva draft y scroll interno;
- no cambia el anchor del transcript;
- mouse y teclado usan los mismos items.

## 12. Permisos, preguntas y planes

### 12.1 Permisos

Conservar:

- inline;
- transcript visible;
- deny preseleccionado;
- riesgo y scope;
- preview con viewport;
- shortcuts explícitos.

Agregar:

- estado de evidencia separado;
- acción por fila completa;
- razón de por qué una opción está disabled;
- copy de comando/path;
- confirmación adicional sólo para scopes persistentes de alto riesgo;
- receipt durable con la decisión, sin secretos.

### 12.2 Presupuesto

```text
PermissionMax = min(
  availableAfterTranscriptMin,
  clamp(floor(H/2), 8, floor(0.8*H)),
)
```

Si el contenido excede el cap:

- header y opciones permanecen visibles;
- sólo el detalle hace scroll;
- el cursor nunca queda fuera del rect del owner.

### 12.3 Preguntas

QuestionForm reemplaza el composer:

- título;
- progreso `2/4`;
- pregunta;
- opciones;
- preview opcional;
- acciones.

Wide usa lista + preview sólo si cada pane conserva su mínimo. Si no, se apila.
El alto del formulario permanece estable al cambiar de tab; el contenido
interno usa viewport.

### 12.4 Plan

Plan inline muestra sólo:

- paso actual;
- progreso;
- bloqueo;
- siguiente acción.

La revisión completa abre un Plan Viewer con TOC, cuerpo y acciones. No debe
empujar el transcript hasta una sola fila.

## 13. Overlays, foco, cursor y mouse

### 13.1 Modal stack

```go
type OverlayProjection struct {
    ID           OverlayID
    Rect         CellRect
    Z            int
    Focus        FocusToken
    PreviousFocus FocusToken
    Cursor       *tea.Cursor
    Dismiss      DismissPolicy
    Scrim        ScrimPolicy
    HitRegions   []HitRegion
}
```

Operaciones:

```text
Push(overlay)
ReplaceTop(overlay)
Pop()
PopTo(id)
ClearNonBlocking()
```

Al hacer `Pop`, se restaura `PreviousFocus` si su owner sigue vivo; de lo
contrario se usa un fallback explícito.

### 13.2 Z-order

```text
base frame
context drawer
non-blocking overlay
blocking overlay
permission/authority owner
terminal recovery
```

Approval no se convierte en un modal genérico dismissable.

### 13.3 Scrim

- terminal ancho: atenuar base fuera del modal;
- `NO_COLOR`: usar contraste de peso/borde, no sólo color;
- reduced motion: sin fade;
- fullscreen: no dibujar base oculta;
- composer subyacente conserva geometría, pero no cursor.

### 13.4 Cursor

Invariantes:

- cero o un cursor;
- pertenece al focus owner;
- está dentro de su rectángulo;
- overlay de texto traduce coordenadas una vez;
- al perder foco, el child desactiva cursor virtual;
- Recovery siempre devuelve cursor `nil`.

### 13.5 Hit regions

```go
type HitRegion struct {
    Rect      CellRect
    Z         int
    ActionID  ActionID
    Entity    EntityRef
    Enabled   bool
}
```

Resolución:

1. filtrar por `Rect.Contains`;
2. elegir mayor `Z`;
3. resolver empates por orden de paint;
4. ejecutar la misma `UIAction` que el teclado.

No se permite un hit target definido sólo por fila y `EndCol`.

### 13.6 Mouse

Paridad mínima:

- click de disclosure;
- click de opción;
- click de fila de picker;
- wheel en owner correcto;
- selección de texto con override documentado;
- hover sólo si no borra estado semántico;
- pointer event sobre overlay nunca llega al transcript.

## 14. Status, header y contexto

### 14.1 Status por prioridad

Orden:

1. seguridad/autoridad;
2. estado de ejecución;
3. localidad del prompt;
4. provider/model;
5. contexto/tokens;
6. shortcuts;
7. metadata decorativa.

En compact:

```text
ASK · REMOTE · 18%
```

En regular:

```text
ASK · provider/model · REMOTE · 18% context · follow paused
```

En compact, `LOCAL`/`REMOTE` tiene prioridad sobre el nombre del modelo. La
localidad nunca se reduce a una palabra de profile que el usuario deba
interpretar.

### 14.2 Header

No se necesita un header de dos filas siempre.

```text
Compact height: 0
Short/Regular height: 1
Tall + rich context: hasta 2
```

La aparición de chrome adicional debe ser monotónica y no reducir el
transcript al crecer una sola fila.

### 14.3 Capa multi-provider

El HEAD actual ya presenta primero la frontera remota y después el modelo
(`internal/ui/model_location.go:5-53`). Ese orden de seguridad debe
preservarse y formalizarse en una proyección:

```go
type ModelPresentation struct {
    ProviderID    ProviderID
    ProviderLabel string
    ModelLabel    string
    Locality      Locality
    Connection    ConnectionState
    Context       ContextUsage
}

type ProviderOptionPresentation struct {
    ProfileID      ProviderID
    Label          string
    KindLabel      string
    ModelLabel     string
    Locality       Locality
    Credential     CredentialState
    CredentialHint string
    Selectable     bool
    DisabledReason string
}
```

`ModelPresentation` sirve al chrome; `ProviderOptionPresentation` sirve al
picker/setup. La UI no recibe clientes, base URLs, headers ni valores de
credenciales. `CredentialHint` puede ser el nombre saneado de una variable de
entorno ausente, nunca su valor. El picker actual ya mantiene esta última
frontera visual (`internal/ui/providerpicker.go:24-40`), aunque hoy construye
la presentación directamente desde `llm.ProviderDescriptor`.

## 15. Wireframes objetivo

Los wireframes muestran jerarquía, no copy final.

### 15.1 `30×12`

```text
assistant
  Fixed the…
│ ▸ ✓ Test · 12 passed

──────────────
AUTO · working
╭────────────╮
│ follow-up… │
╰────────────╯
```

Propiedades:

- sin header;
- sin panel;
- transcript mínimo;
- tool en una fila;
- status condensado;
- composer con scroll interno;
- overlays complejos usan fullscreen.

### 15.2 `80×24`

```text
workspace · provider/model

you
▌ Improve the TUI

assistant
  I am reviewing the layout…
│ ▾ ◉ Experts · 2 running
│   ◉ Crush audit       layout
│   ◉ Grok audit        tools

│ ▸ ✓ Read · internal/ui/view.go · 223 lines

──────────────────────────────────────────────────────────────
AUTO · working · 18% context · follow latest
╭────────────────────────────────────────────────────────────╮
│ Write a follow-up…                                         │
╰────────────────────────────────────────────────────────────╯
```

No aparece sidebar sólo por llegar a 80.

### 15.3 `120×30`

```text
workspace · branch                          provider/model · 18%

you
▌ Improve tools and agents

assistant
  Prose remains close to 96 columns even though work surfaces are wider.

│ ▾ ✓ Edit · internal/ui/layout.go · +28 -7
│     41  41 │  context
│     42     │ -old line
│         42 │ +new line
│            │ ↳ wrapped continuation

│ ▸ ✓ Experts · 4 completed · open Agents

────────────────────────────────────────────────────────────────────────
ASK · done · follow latest
╭──────────────────────────────────────────────────────────────────────╮
│ Ask, @mention files, or type /help                                   │
╰──────────────────────────────────────────────────────────────────────╯
```

El ancho extra beneficia tools y diffs; la prosa no se estira.

### 15.4 `160×40` con Agent Hub dockeado por el usuario

```text
┌──────────────────────────── chat ───────────────────────┬─ Agents ────────┐
│ workspace · branch                       provider/model │ 2 running        │
│                                                        │                  │
│ you                                                    │ ◉ TUI audit      │
│ ▌ Implement the next phase                             │   reading layout │
│                                                        │                  │
│ assistant                                              │ ◉ Tests          │
│   Working through the frame projection…                │   ui suite       │
│                                                        │                  │
│ │ ▸ ◉ Edit · internal/ui/frame.go                      │ ✓ Research       │
│                                                        │                  │
│                                                        │ [view] [cancel]  │
├────────────────────────────────────────────────────────┴──────────────────┤
│ AUTO · working · follow latest · 21% context                              │
│ ╭────────────────────────────────────────────────────────────────────────╮ │
│ │ Write a follow-up…                                                     │ │
│ ╰────────────────────────────────────────────────────────────────────────╯ │
└────────────────────────────────────────────────────────────────────────────┘
```

El composer y status siguen full-width. El panel no aparece automáticamente.

### 15.5 Permiso

```text
assistant
  I need permission to continue.

────────────────────────────────────────────────────────────────
Permission required · high risk
Command may modify files outside the workspace.

scope    /path
command  …

  Allow once
  Allow for this command
▸ Deny

↑↓ move · Enter choose · Esc deny
```

El detalle largo tiene viewport; título, riesgo y opciones no desaparecen.

### 15.6 Agent Viewer

```text
Parent run / TUI audit                         running · 42 s
researcher · model · 12k tokens                          [close]
────────────────────────────────────────────────────────────────
assistant
  Inspecting geometry…
│ ▸ ✓ Read · internal/ui/layout.go
│ ▸ ◉ Test · internal/ui

────────────────────────────────────────────────────────────────
/ search · PgUp/PgDn scroll · c cancel · Esc back
```

## 16. Temas y accesibilidad

### 16.1 Paleta

Usar sólo roles derivados de `lipgloss.LightDark()`:

- foreground;
- muted;
- dim;
- accent;
- focus;
- running;
- success;
- warning;
- error;
- selected background;
- scrim.

No introducir ANSI hardcoded.

### 16.2 `NO_COLOR`

En `NO_COLOR`:

- estado sigue visible por glifo y label;
- selección usa borde/peso;
- diffs conservan `+`, `-` y gutters;
- permisos conservan riesgo textual;
- links y acciones tienen disclosure;
- scrim puede usar dim/bold o relleno neutro.

### 16.3 Movimiento reducido

Una sola política global gobierna:

- spinner;
- cursor blink;
- shimmer;
- reveal;
- title animation;
- transiciones.

Running usa un glifo estático más label. No debe haber componentes con su
propio flag desconectado.

### 16.4 Perfil de glifos

```go
type GlyphProfile uint8

const (
    GlyphUnicode GlyphProfile = iota
    GlyphASCII
)
```

Ejemplos:

| Semántica | Unicode | ASCII |
|---|---|---|
| user rail | `▌` | `|` |
| collapsed | `▸` | `>` |
| expanded | `▾` | `v` |
| success | `✓` | `+` |
| error | `✗` | `x` |
| running | `◉` | `*` |
| continuation | `↳` | `>` |

El perfil se prueba; no es un search/replace tardío.

### 16.5 Contraste

La matriz debe cubrir:

- tema claro y oscuro;
- texto normal/muted/dim;
- success/warning/error;
- selección de lista;
- focus;
- disabled;
- chips;
- tool rails;
- diff foreground y background;
- scrim;
- `NO_COLOR`.

### 16.6 IME y Unicode

- el cursor usa celdas, no bytes;
- truncado es por ancho terminal;
- wrap respeta graphemes;
- paste no fragmenta secuencias;
- hit testing usa columnas pintadas;
- no reintroducir el helper byte-based actual.

## 17. Persistencia y recuperación

### 17.1 Estado durable mínimo

Persistir:

- `BlockID`, `TurnID`, kind, lifecycle final;
- mensajes visibles;
- proyección semántica de tool;
- diff metadata/artifact ref;
- WorkNode final bounded;
- provider profile ID, model ID y localidad de la sesión;
- expand/collapse cuando sea útil;
- anchors por sesión;
- preferencia de panel.

No persistir:

- spinner frame;
- `ResultDisplay` ANSI;
- `StructuredContent`;
- focus transitorio;
- hit rects;
- medidas de layout;
- razonamiento privado;
- prompts internos de agentes;
- API keys, headers, base URLs o valores de variables de entorno.

La preferencia global de provider sigue siendo distinta del provider de una
sesión. El HEAD actual persiste `Model`, pero no identidad de provider en
`persistedSessionState` (`internal/ui/session.go:98-118`).

El contrato nuevo necesita:

```go
type SessionProviderRef struct {
    ProfileID ProviderID
    Kind      ProviderKind
    ModelID   string
    Locality  Locality
}
```

No contiene endpoint ni credencial.

### 17.2 Restore de sesión

Política:

1. resolver `SessionProviderRef` sin hacer una llamada de red;
2. comprobar que el profile sigue configurado;
3. comprobar disponibilidad de credencial mediante estado booleano/saneado;
4. validar que el modelo sigue permitido;
5. construir transcript y receipts fuera del estado visible;
6. intercambiar sesión, provider runtime y bloques de forma atómica;
7. no enviar ningún prompt durante restore.

Si el provider/model no está disponible:

- no hacer fallback silencioso;
- no cambiar la preferencia global;
- abrir un `ProviderRecovery` como owner del composer;
- permitir abrir la sesión read-only;
- permitir mapear explícitamente a otro provider/model;
- conservar un receipt que explique el cambio si el usuario lo acepta.

Para sesiones legacy sin `SessionProviderRef`:

- usar la preferencia actual sólo como contexto de lectura;
- mostrar que la identidad original del provider no está disponible;
- no reanudar automáticamente una operación;
- exigir elección antes del siguiente prompt si el modelo/context receipt no
  puede validarse.

Tests obligatorios:

- guardar con provider A, cambiar preferencia global a B y restaurar;
- provider eliminado;
- key ausente;
- modelo renombrado/no admitido;
- provider local frente a remoto;
- restore read-only;
- mapping explícito;
- ningún valor de credencial en JSON, transcript o notice.

### 17.3 Restore de checkpoint

El checkpoint actual ya tiene un envelope v1 con mensajes y
`ContextPromptFloor`, restaura ambos bajo un lock y lee el formato legacy v0
(`internal/agent/checkpoint.go:21-31`,
`internal/agent/checkpoint.go:59-110`,
`internal/agent/checkpoint.go:128-163`). La brecha no es la ausencia total de
versionado o atomicidad del agente; es que el envelope no incluye la identidad
visual del TUI.

Una versión siguiente debe extender, no reemplazar, esas garantías:

```go
type TranscriptCheckpointEnvelopeV2 struct {
    Version            int
    Provider           SessionProviderRef
    Messages           []llm.Message
    ContextPromptFloor agent.ContextPromptFloor
    Blocks             []PersistedBlock
    ToolReceipts       []PersistedToolReceipt
    WorkNodes          []PersistedWorkNode
    LastSequence       uint64
}
```

Invariantes:

- `Messages`, `ContextPromptFloor`, bloques y receipts corresponden al mismo
  corte lógico;
- `BlockID`, `InvocationID` y `WorkNodeID` se conservan;
- el envelope se valida completo antes de modificar estado visible;
- el swap de `entries`, `toolEntries`, `BlockStore`, provider ref y anchor es
  atómico desde la perspectiva del event loop;
- restore repetido es idempotente y no duplica bloques;
- una tool/agente live se restaura como `interrupted`/`cancelled`, nunca vuelve
  a ejecutar un side effect;
- `LastSequence` impide aceptar receipts tardíos del timeline anterior;
- `ContextPromptFloor` sigue fallando cerrado si provider/model no coincide;
- se aplican los mismos límites y redacciones que a una sesión;
- versión desconocida falla sin mutar el frame;
- v1 restaura mensajes/floor y muestra que receipts visuales no estaban
  disponibles; no los fabrica;
- v0 conserva la política legacy conservadora existente.

Tests:

- restore con tools settled, failed y attention;
- tool live al capturar;
- equipo de agentes parcial;
- IDs/índices después de dos restores;
- receipt tardío después del rewind;
- límite de bytes/filas;
- path, ANSI, raw MCP y credenciales redactados;
- migración v0/v1;
- provider/model incompatible;
- fallo de validación deja exactamente el frame anterior.

El pipeline común queda:

```text
session/checkpoint
  ↓
envelope validado y saneado
  ↓
BlockStore + Tool/Agent adapters
  ↓
LayoutIndex al ancho actual
```

La brecha de checkpoints debe resolverse sin degradar la persistencia nativa de
sesiones que ya conserva tools.

### 17.4 Reconciler

El reconciler asigna:

- IDs;
- orden causal;
- revisión;
- lifecycle;
- dirty flags;
- receipt final.

No debe escribir frames del terminal ni decidir color.

## 18. Integración con la capa multi-provider

### 18.1 Frontera temporal

El primer commit multi-provider ya aterrizó en `0a84364b`. Eso permite auditar
la frontera, pero no equivale por sí solo a una confirmación de que la capa
está lista para construir encima. La implementación de este rediseño comienza
sólo cuando el responsable del proyecto confirme explícitamente su
estabilización.

No mezclar P0 con cambios todavía abiertos de providers. Ambos frentes tocan:

- status;
- labels de modelo;
- session restore;
- eventos de streaming;
- lifecycle;
- tool metadata;
- errores y recovery.

Implementarlos simultáneamente haría difícil distinguir fallos de contrato de
fallos de layout.

### 18.2 Condiciones para comenzar

Antes de P0:

- configuración multi-provider estabilizada;
- selección y cambio de provider probados;
- provider picker probado en resize, cambio de tema, mouse, wheel y
  `ManualAnchor`, con reduced motion aplicado al filtro;
- switch de provider ejecutado como `tea.Cmd` cancellable; `Update` no espera
  discovery, refresh local ni locks del runtime;
- IDs de modelo/provider presentables;
- streaming normalizado;
- errores normalizados;
- tool events con IDs estables;
- restore de sesión probado con más de un provider;
- tests actuales verdes;
- documentación de producto para `docs/` separada de este estudio de
  ingeniería del repositorio.

### 18.3 Auditoría de reentrada

Durante esta redacción ya se completó una primera reentrada:

- se fijó `0a84364b` como HEAD final del estudio;
- se comparó el delta desde `6b3246b`;
- se revisaron provider picker, overlay, status/locality, preferencias,
  completion y switch;
- se actualizaron los supuestos y referencias afectados.

El delta actual prueba manager/switch y key ausente, pero todavía no
caracteriza toda la interacción TUI del picker ni la identidad de provider en
restore de sesión. Esos puntos siguen siendo gates, no trabajo ya demostrado.

Cuando se autorice la implementación:

1. fijar otra vez el HEAD real;
2. revisar cualquier cambio posterior en `internal/llm`, config, startup,
   session, provider picker y status;
3. actualizar este documento si cambió algún supuesto;
4. crear tests de caracterización antes de mover layout;
5. implementar P0 sin mezclar features visuales P1.

### 18.4 Contrato provider-neutral para UI

La UI debería consumir eventos como:

```go
type ProviderUIEvent interface {
    RunRef() RunRef
    Sequence() uint64
}

type ModelStateProjection struct {
    ProviderID   string
    ProviderName string
    ModelID      string
    ModelName    string
    Connection   ConnectionState
    Context      ContextUsage
}
```

No debe hacer type switches sobre Ollama, OpenAI-compatible u otros providers
para decidir geometría.

## 19. Arquitectura Charm propuesta

### 19.1 Componentes

| Necesidad | Charm |
|---|---|
| app loop | Bubble Tea v2 |
| composer | Bubbles v2 `textarea` |
| search/filter | Bubbles v2 `textinput` |
| transcript/inspectores | Bubbles v2 `viewport` como adaptador de ventana |
| listas/pickers/Agent Hub | Bubbles v2 `list` |
| tablas de metadata | Bubbles v2 `table` cuando sea apropiado |
| actividad | Bubbles v2 `spinner` o glifo estático con reduced motion |
| estilos/layout | Lip Gloss v2 |
| Markdown | Glamour con cache por sección |

No crear componentes custom si Bubbles ya resuelve input, viewport, list,
table, progress, timer o key binding.

### 19.2 Paquetes sugeridos

```text
internal/ui/
  frame/
    geometry.go
    layout.go
    projection.go
    capabilities.go
  transcript/
    block.go
    store.go
    reconcile.go
    layout_index.go
    anchor.go
    search.go
  surfaces/
    chat.go
    composer.go
    status.go
    permission.go
    question.go
  tools/
    common.go
    read.go
    search.go
    exec.go
    edit.go
    generic.go
  agents/
    projection.go
    group.go
    hub.go
    viewer.go
  overlay/
    stack.go
    focus.go
    hit.go
```

Esta estructura es una dirección, no una obligación de hacer un big-bang
move. La migración puede comenzar dentro de los archivos existentes.

### 19.3 Estado compartido

Fuera del event loop:

- caches compartidos usan `sync.RWMutex`;
- operaciones cancellables reciben `context.Context`;
- render no toma locks largos;
- resultados async regresan como `tea.Msg`;
- un receipt tardío se correlaciona por `RunRef + InvocationID`.

### 19.4 Dirty graph

```go
type DirtyFlags uint32

const (
    DirtyMeasure DirtyFlags = 1 << iota
    DirtyPaint
    DirtySearch
    DirtyHitRegions
    DirtyAnchor
)
```

Cambios:

- spinner: paint del bloque live;
- nuevo chunk: measure/paint/search del bloque;
- resize: measure de bloques visibles y anchor;
- theme: paint de visibles, Markdown cache por theme;
- expand: measure/paint/hit/anchor del bloque;
- overlay: frame/focus/hit, no transcript content.

## 20. Secuencia de implementación

### Fase 0 — después de multi-provider

Objetivo: congelar comportamiento actual.

- actualizar auditoría;
- tests de caracterización de `View`, `footerHeight`, cursor, approvals, cola,
  provider picker y status local/remoto;
- fixtures de cambio de provider, provider sin key, persistencia de preferencia
  y combinación provider/model;
- fixtures `30×12`, `40×16`, `72×24`, `80×24`, `112×24`, `120×30`,
  `160×48`, `200×60` y un caso derivado con `A=131`, `H=40`;
- registrar baseline de tiempo y allocations;
- no cambiar estética.

### P0.1 — geometría y frame único

- `CellRect`;
- splits seguros;
- `WidthClass` y `HeightClass`;
- `LayoutCapabilities`;
- `FrameProjection`;
- un solo cálculo de owner/cursor/hit rects;
- retirar medición duplicada de `footerHeight()`;
- conservar exactamente el composer y approval actuales.

Criterio de salida:

- `View()` no llama renderers para volver a medir;
- frame y hit regions provienen de un snapshot;
- todos los tamaños soportados caben;
- tests actuales siguen verdes.

### P0.2 — identidad y anchor

- `BlockID`, `TurnID`, `Revision`, lifecycle;
- adaptador de `ChatEntry` a `TranscriptBlock`;
- anchor semántico;
- `LineMap`;
- restore de checkpoint con tool receipts;
- cache por ID, no índice.

Criterio de salida:

- resize conserva `BlockID + LogicalOffset` y `ScreenRow` cuando los límites lo
  permiten; en bordes aplica clamp/fallback determinista;
- streaming respeta la compensación por delta y nunca salta a contenido no
  relacionado;
- expand/collapse no pierde el bloque;
- restore conserva la gramática de tools.

### P0.3 — reconciler y paint window

- una ruta de actualización del transcript;
- reducir llamadas dispersas a `SetContent(renderEntries())`;
- layout index;
- rango visible;
- cache exacto para visibles;
- no O(N) en spinner tick.

### P1.1 — lectura y tools

- prosa acotada, usando 96 como candidato inicial;
- secciones Markdown;
- renderers Read/Search/Exec/Edit/Generic;
- previews por filas;
- acciones comunes;
- eliminar truncado byte-based.

### P1.2 — Diff Viewer y output viewer

- viewport;
- navegación por archivo/hunk;
- wrap;
- intralínea;
- unified/split gated;
- search/copy;
- artifact paging.

### P1.3 — overlays

- `ModalStack`;
- focus token;
- scrim;
- click parity;
- action registry;
- help derivado del registry.
- integrar provider picker en resize, restyle, mouse, wheel y preservación de
  `ManualAnchor`.

### P1.4 — Agents

- `WorkNode`;
- adaptador de expertos;
- Agent Group inline;
- Agent Hub;
- Agent Viewer;
- acciones cancel/view/jump;
- panel wide opt-in.

### P2 — profundidad

- búsqueda de transcript;
- selección semántica;
- sticky turn headers opcionales;
- perfil ASCII;
- matriz completa de contraste;
- snapshots PTY;
- split diff refinado;
- contextual panel persistente.

## 21. Plan de tests

### 21.1 Propiedades geométricas

Barrido:

```text
W = 0..400
H = 0..400
```

Para cada estado representativo:

- ningún rect negativo;
- rects base disjuntos;
- todo rect dentro de `SafeScreen`;
- cursor dentro de su owner;
- hit rect dentro de su surface;
- máximo un focus owner;
- máximo un cursor;
- rendered width `<= W`;
- rendered height `<= H`;
- transcript mínimo preservado;
- monotonía horizontal y vertical sin acciones explícitas.

### 21.2 Matriz visual

| Tamaño | Propósito |
|---|---|
| `30×12` | mínimo |
| `39×15` | borde Compact |
| `40×16` | entrada Narrow/Short |
| `71×23` | fin Narrow/Short |
| `72×24` | entrada Regular |
| `80×24` | caso mediano real |
| `111×40` | antes de Wide |
| `112×24` | Wide sin auto-dock |
| `120×30` | terminal cómodo |
| `A=131`, `H=40` | panel 32 + candidato de prosa 96 después de insets/padding |
| `160×48` | wide |
| `200×60` | stress visual |

Variantes:

- tema claro/oscuro;
- `NO_COLOR`;
- reduced motion;
- Unicode/ASCII;
- empty/streaming/error/permission;
- tools collapsed/expanded;
- 0/1/8/16 agentes;
- overlay;
- follow/manual anchor;
- provider picker local/remoto/key ausente;
- resize y cambio de tema con provider picker abierto.

### 21.3 Transcript

- orden causal text → tool → text;
- separadores exactos;
- ID estable;
- revisión monotónica;
- settled no vuelve a live;
- streaming sólo ensucia su bloque;
- restore V2 produce los mismos IDs y bloques;
- restore legacy conserva mensajes y añade notice explícito;
- búsqueda apunta al offset correcto;
- bloque eliminado aplica fallback.

### 21.4 Scroll

- resize 200→80→120 conserva anchor;
- expansión encima compensa;
- expansión debajo no mueve;
- nuevo streaming no roba lectura;
- FollowLatest permanece al final;
- overlay no cambia anchor;
- confirmar provider conserva `ManualAnchor`;
- Agent Viewer tiene anchor independiente.

### 21.5 Tools

- todas las combinaciones transporte/dominio/evidencia;
- success no se deriva sólo de transporte;
- error visible colapsado;
- unknown distinto de success/error;
- previews respetan filas;
- output hidden count correcto;
- diff wrap conserva gutters;
- split no aparece sin ancho;
- `NO_COLOR` conserva significado.

### 21.6 Agents

- parent/child;
- orden estable;
- running primero;
- `+N`;
- cancel disabled con razón;
- viewer no muta chat;
- steer sólo si capability;
- no prompt/reasoning en snapshot;
- restore de estado final.

### 21.7 Foco e input

- cada owner consume sus teclas;
- permiso gana a overlay;
- overlay no filtra teclas;
- pop restaura foco;
- click y keyboard ejecutan mismo `ActionID`;
- wheel mueve owner correcto;
- un cursor;
- IME/paste Unicode.

### 21.8 Privacidad y fronteras negativas

- `StructuredContent` MCP crudo no entra en ningún `TranscriptBlock`;
- MCP crudo no entra en search, clipboard, export, sesión ni checkpoint;
- API keys, headers, base URLs y valores de entorno no aparecen en UI ni
  persistencia;
- el nombre de una variable de entorno se sanea, limita y trata como metadato;
- prompts internos y razonamiento privado no entran en Agent Group, Hub,
  viewer, reportes ni restore;
- paths privados no autorizados se redactan antes de crear una proyección;
- ANSI crudo no se persiste ni llega al terminal;
- un envelope manipulado falla cerrado y no sustituye estado visible;
- restore legacy no fabrica evidencia ni tool receipts;
- los tests inspeccionan el modelo semántico y el JSON durable, no sólo el
  string final de `View()`.

### 21.9 Rendimiento

Presupuestos propuestos, todavía no benchmarks medidos:

- spinner tick: coste proporcional a bloques visibles/live, no historial;
- resize: medir visibles + margen antes de trabajo diferido;
- search index: actualizar sólo revisiones dirty;
- Markdown settled: una renderización por ancho/theme/revisión;
- animación: máximo 10 Hz, cero ticks decorativos con reduced motion;
- memoria de alturas: bounded y observable;
- viewers de output: páginas bounded.

Los números de latencia deben fijarse después de medir el baseline en la
máquina de CI; no se presentan aquí como resultados existentes.

## 22. Criterios de aceptación de producto

La implementación hace match o supera a los referentes cuando:

- abrir/cerrar/resizear una superficie conserva el punto semántico anclado y,
  cuando cabe, su `ScreenRow`; en bordes aplica el fallback documentado;
- la UI conserva al menos cuatro filas de transcript en el mínimo soportado;
- una terminal más ancha nunca estrecha el chat sin acción del usuario;
- a `80×24` chat, composer y permiso siguen siendo plenamente utilizables;
- prosa ancha es legible y code/diffs aprovechan el espacio;
- una tool siempre comunica estado y, cuando existe artifact/detail retenido,
  ofrece navegación; si no existe, lo declara sin chevron falso;
- transporte, dominio y evidencia permanecen separados;
- MCP crudo, credenciales, prompts/razonamiento privado y paths no autorizados
  nunca entran en transcript, search, clipboard, export, sesión o checkpoint;
- un equipo de agentes tiene resumen inline, Hub y viewer;
- navegar a un hijo no altera el chat principal;
- foco, cursor, mouse y paint usan la misma geometría;
- restore conserva tools y agentes asentados;
- `NO_COLOR`, reduced motion y ASCII mantienen jerarquía;
- spinner y streaming no recorren todo el historial.

## 23. Qué no copiar

### De Crush

- offsets distintivos;
- breakpoints exactos;
- copy y símbolos propios;
- implementación bajo FSL;
- hit testing por fila sin X inicial.

### De Oh My Pi

- renderer ANSI;
- scrollback nativo como núcleo;
- complejidad de replay terminal;
- permisos demasiado binarios.

### De Zero

- editor híbrido;
- sidebar automático;
- decisiones de tier por ancho total después de reducir el pane;
- footer que recorta desde arriba hasta dejar una fila de transcript;
- overlay que cambia el layout base.

### De Grok Build

- arquitectura completa de Ratatui;
- todos los caches desde la primera fase;
- complejidad de sticky headers antes de tener BlockID;
- nombres, copy o snapshots.

### De Local Agent actual

- doble medición;
- `YOffset` como identidad de lectura;
- `SetContent(renderEntries())` disperso;
- memo por índice;
- truncado por bytes;
- tools restauradas sólo en algunos caminos;
- “Agent” como nombre tanto de perfil como de ejecución.

## 24. Recomendaciones de base y preguntas abiertas

Nada en esta sección es una decisión arquitectónica vinculante. Son defaults
recomendados para prototipar. Si el proyecto los adopta como contrato estable,
la decisión correspondiente debe registrarse fuera de este repositorio en la
ubicación de ADR definida por `AGENTS.md`.

### Defaults recomendados para prototipo

- Charm v2 sigue siendo la base.
- No habrá sidebar automático.
- Prosa y trabajo tendrán medidas distintas.
- Unified diff será default.
- Permissions seguirán inline y fail-closed.
- `consult_experts` será el primer adaptador de agentes.
- Agent Viewer será read-only por default.
- El contexto wide sólo se dockea por elección.
- La implementación espera a la estabilización multi-provider.

### Por validar con prototipo

- `ProseWidth` exacto: 96 frente a 104/112.
- Insets terminales por plataforma.
- panel `28/32/36`.
- cap inline de Agent Group: 6 u 8 filas.
- ancho mínimo de split diff: 52 o 56 por pane.
- frecuencia visual de actividad: 8 o 10 Hz.
- sticky turn header como opción P2.
- persistencia de estado expand/collapse por sesión.

Estas decisiones se validan con fixtures y uso real; no con preferencia
estética aislada.

## 25. Índice de evidencia

### Crush

- compact mode y frame:
  `internal/ui/model/ui.go:3009-3285`
- textarea/editor:
  `internal/ui/model/ui.go:81-92`, `internal/ui/model/ui.go:3095-3113`
- mensajes y medida:
  `internal/ui/chat/messages.go:21-26`, `internal/ui/chat/messages.go:357-360`
- tools:
  `internal/ui/chat/tools.go`
- agentes:
  `internal/ui/chat/agent.go`
- permisos:
  `internal/ui/dialog/permissions.go`

### Oh My Pi

- raíz interactiva:
  `packages/coding-agent/src/modes/interactive-mode.ts:675-940`
- transcript y seam:
  `packages/coding-agent/src/modes/components/transcript-container.ts`
- renderer:
  `packages/tui/src/tui.ts`
- Agent Hub/viewer:
  `packages/coding-agent/src/modes/components`
- overlays:
  `packages/tui/src/tui.ts:1406-1484`

### Zero

- tiers:
  `internal/tui/view.go:28-50`
- sidebar:
  `internal/tui/sidebar.go:25-147`
- frame:
  `internal/tui/model.go:2872-2947`
- transcript:
  `internal/tui/transcript_selection.go`
- cache:
  `internal/tui/transcript_body_cache.go`
- tools/diffs:
  `internal/tui/rendering.go`

### Grok Build de xAI

- layout puro y terminal corto:
  `crates/codegen/xai-grok-pager/src/views/agent.rs:80-258`
- tokens espaciales:
  `crates/codegen/xai-grok-pager-render/src/appearance/config.rs:189-225`
- scrollback:
  `crates/codegen/xai-grok-pager/src/scrollback`
- búsqueda:
  `crates/codegen/xai-grok-pager/src/scrollback/search.rs`
- tools:
  `crates/codegen/xai-grok-pager/src/scrollback/blocks/tool`
- tasks/subagents:
  `crates/codegen/xai-grok-pager/src/views/tasks_pane.rs`

### Local Agent

- layout actual:
  `internal/ui/layout.go`
- frame:
  `internal/ui/view.go`
- doble medición:
  `internal/ui/model.go:1055-1122`
- transcript:
  `internal/ui/view_transcript.go`
- resize:
  `internal/ui/update_terminal.go`
- scroll:
  `internal/ui/scroll_follow.go`
- tool cards:
  `internal/ui/toolcard.go`
- diffs:
  `internal/ui/diff.go`
- expertos:
  `internal/ui/expert_progress.go`
- approvals:
  `internal/ui/approval.go`
- provider projection:
  `internal/ui/provider.go`, `internal/ui/model_location.go`
- provider picker:
  `internal/ui/providerpicker.go`
- switch sincrónico de provider:
  `internal/ui/provider.go:44-48`, `internal/llm/manager.go:181-224`
- reduced motion del filtro:
  `internal/ui/providerpicker.go:52-76`, `internal/ui/picker_style.go:102`
- ownership de teclado del provider picker:
  `internal/ui/model.go:828-833`, `internal/ui/keys_overlay.go:141-152`
- resize/restyle de pickers:
  `internal/ui/settingspicker.go:145-215`,
  `internal/ui/picker_style.go:115-158`
- preferencia provider/model:
  `internal/ui/model_preference.go`
- estado durable de sesión:
  `internal/ui/session.go:98-118`
- envelope de checkpoint:
  `internal/agent/checkpoint.go:21-163`
- restore de checkpoint:
  `internal/ui/update_command.go:477-501`

## 26. Checkpoint de implementación

Este apartado registra estado observado, no una ADR ni una promesa de
compatibilidad. La implementación convive con el trabajo multi-provider que ya
estaba en curso y no atribuye esos cambios al rediseño del TUI.

### Implementado

- `CellRect`, `FrameProjection` y tests geométricos de frame, ownership y
  reflow;
- identidad semántica `BlockID`/`TurnID`/`Revision`, anchors y restore de
  checkpoints con receipts;
- reconciliación y caches de transcript fuera de los ticks puramente visuales;
- `ToolViewModel` con transporte, dominio y evidencia separados;
- `WorkNode` y adaptador bounded de `consult_experts`;
- Agent Group inline con máximo de seis filas y disclosure honesto del resto;
- Agent Hub read-only con `/agents` y `Ctrl+G`;
- Agent Viewer con viewport, identidad estable por grupo/nodo, scroll semántico
  al cambiar ancho o progreso y salto al bloque causal del transcript;
- geometría y mouse half-open, filtro síncrono bounded, foco explícito y
  preemption por approvals/Cortex;
- estados empty, unavailable, awaiting plan, live, settled, failed e
  interrupted sin convertir éxito de transporte en éxito de dominio;
- privacidad fail-closed: prompts, objetivos, reasoning, reportes, errores
  crudos, paths, argumentos, resultados y `StructuredContent` no entran en la
  proyección del Hub.

### Defaults ya validados por el primer vertical slice

- seis filas inline antes de mover el detalle al Hub;
- Hub y Viewer read-only mientras el runtime no publique capacidades exactas
  de cancel/steer;
- modal centrado hasta 68 celdas, usable en `30×12`, `40×16`, `72×24` y
  `112×40`;
- Viewer de una fila por nodo cuando caben identidad y metadata; dos filas en
  ancho estrecho;
- hints derivados del modo real: empty, filtering vacío/no vacío,
  filter-applied con/sin matches, Hub y Viewer;
- selección inicial del grupo live más reciente y, si no existe, del settled
  más reciente.

### Evidencia del checkpoint

- suite Go completa y `go vet` verdes;
- tests con race detector para Agent Hub, Agent Viewer, Agent Surface,
  ExpertProgress y WorkNode;
- fixture Ollama loopback que recorre padre → `consult_experts` → experto sin
  tools → follow-up correlacionado;
- contrato Glyph `tui_agents.yml` con ocho outcomes;
- los diecinueve contratos Glyph del repositorio verdes;
- refresh del Hub sobre un transcript de 10 000 entradas alrededor de 41–42
  microsegundos y 5 KB por operación al reutilizar la reconciliación ya
  validada por el renderer, frente a aproximadamente 2 ms y 2.9 MB al
  reconciliar dos veces.

### Pendiente; no debe presentarse como terminado

- renderers especializados de Read/Search/Exec/Edit/Generic;
- Output Viewer y Diff Viewer con navegación, búsqueda y copy;
- `ModalStack` y action registry generalizados;
- búsqueda y selección semántica del transcript;
- pane contextual wide opt-in;
- cancel/steer de agentes sólo si el runtime publica capacidades y receipts
  verificables;
- cobertura P3 adicional de mouse con paginación/filtros reordenados y una
  aserción específica de Agent Hub bajo `NO_COLOR`.

## Conclusión

La próxima base del TUI debe construirse alrededor de identidad y geometría,
no alrededor de más condiciones de render.

El orden correcto es:

1. confirmar explícitamente la estabilización de multi-provider;
2. convertir el frame en una sola proyección;
3. dar identidad estable al transcript y a su scroll;
4. especializar inspección de tools y diffs;
5. generalizar expertos a agentes, Hub y viewer;
6. profundizar búsqueda, selección y accesibilidad.

Así se conservan las fortalezas reales de Local Agent y se incorporan las
mejores ideas observadas sin copiar la personalidad ni la implementación de
ningún otro harness.
