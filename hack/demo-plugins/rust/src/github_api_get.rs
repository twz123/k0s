use bytes::BufMut;
use wasi::{
    http::{outgoing_handler, types as http_types},
    io::streams,
};

pub(crate) fn print(println: &dyn Fn(&dyn std::fmt::Display) -> ()) {
    let data = match github_api_get() {
        Ok(data) => data,
        Err(e) => {
            println(&format_args!("!\tError while getting GitHub API: {e}"));
            return;
        }
    };

    match serde_json::to_string_pretty(&data) {
        Ok(data) => println(&data),
        Err(e) => println(&format_args!(
            "!\tError while pretty printing GitHub data: {e}"
        )),
    };
}

fn github_api_get() -> anyhow::Result<serde_json::Value> {
    let req = outgoing_handler::OutgoingRequest::new(http_types::Headers::from_list(&[(
        String::from("User-Agent"),
        Vec::from(b"k0s-demo-plugin"),
    )])?);

    req.set_scheme(Some(&http_types::Scheme::Https))
        .expect("HTTPS");
    req.set_method(&http_types::Method::Get).expect("GET");
    req.set_authority(Some("api.github.com"))
        .expect("is a valid DNS name");
    req.set_path_with_query(Some("/")).expect("accepts /");

    let resp = block_on_response(req, None)?;
    match resp.status() {
        200 => (),
        status => anyhow::bail!("status code is {status}"),
    };

    let body = resp.consume().expect("consuming body only once");
    let data: bytes::BytesMut = {
        let stream = body.stream().expect("consuming body stream only once");
        let mut buf = bytes::BytesMut::new();
        loop {
            match stream.blocking_read(16384) {
                Ok(chunk) => buf.put(chunk.as_slice()),
                Err(streams::StreamError::Closed) => break,
                Err(streams::StreamError::LastOperationFailed(e)) => {
                    anyhow::bail!(e.to_debug_string());
                }
            };
        }
        buf
    };

    match serde_json::from_slice::<serde_json::Value>(&data[..]) {
        Ok(value) => return Ok(value),
        Err(e) => {
            eprintln!("Failed to parse JSON: {e}\n{data:?}");
            return Err(e.into());
        }
    }
}

fn block_on_response(
    request: outgoing_handler::OutgoingRequest,
    options: impl Into<Option<outgoing_handler::RequestOptions>>,
) -> anyhow::Result<http_types::IncomingResponse> {
    let resp = outgoing_handler::handle(request, options.into())?;
    resp.subscribe().block();
    Ok(resp
        .get()
        .expect("called block() before")
        .expect("getting response only once")?)
}
