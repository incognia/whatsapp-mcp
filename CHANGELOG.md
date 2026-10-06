# Historial de cambios

**Nota:** Todas las fechas están en zona horaria CST de Ciudad de México (UTC-6).

<!-- markdownlint-disable MD013 MD024 MD022 MD032 -->
## [2026-10-06] - Menciones al enviar y ruta fija de `store/`

- feat: aceptar en `/api/send` una lista opcional `mentions` y enviarla como mensaje de texto extendido con `ContextInfo.MentionedJID`, para que las etiquetas de grupo notifiquen a la persona mencionada
- feat: exponer el parámetro `mentions` en la herramienta `send_message` del servidor MCP
- fix: usar siempre la carpeta `store/` junto al binario del *bridge*, sin importar el directorio desde donde se ejecute; antes, arrancarlo desde otra ruta creaba una sesión nueva que el servidor MCP no leía
- chore: ignorar en Git el binario compilado `whatsapp-bridge/whatsapp-bridge`

## [2026-10-03] - Remitentes de grupo y menciones con LID

- fix: leer el remitente de los mensajes de grupo desde `WebMessageInfo.participant` cuando la llave del mensaje no lo trae, como ocurre en las sincronizaciones de historial recientes; antes se guardaba el grupo como remitente
- feat: traducir las menciones «@<LID>» del texto a «@<número>» al guardar mensajes y corregir al arrancar las ya almacenadas
- feat: mostrar en el servidor MCP «@<nombre>» en lugar de «@<número>» al formatear mensajes, y «@Me» para las menciones propias

## [2026-10-02] - Compatibilidad con whatsmeow actual y direccionamiento LID

- fix: actualizar `whatsmeow` a `v0.0.0-20260929112325` para resolver el error de conexión «Client outdated (405)», pasando `context.Context` a las llamadas de su API actualizada
- fix: traducir los JID con LID (`@lid`) a su número de teléfono en chats y remitentes, tanto en mensajes en vivo como en la sincronización de historial, para que una misma conversación no quede dividida en dos chats
- feat: unir al arrancar los chats y remitentes ya guardados con LID en sus equivalentes por número de teléfono
